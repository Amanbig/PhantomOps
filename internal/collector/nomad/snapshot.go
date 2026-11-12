package nomad

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"

	"PhantomOps/internal/httpx"
)

// Snapshot is the current state of a Nomad cluster.
type Snapshot struct {
	Type          string           `json:"type"` // "nomad"
	TakenAt       time.Time        `json:"takenAt"`
	Version       string           `json:"version"`
	Region        string           `json:"region"`
	Datacenter    string           `json:"datacenter"`
	Nodes         []Node           `json:"nodes"`
	Jobs          []Job            `json:"jobs"`
	ProblemAllocs []Alloc          `json:"problemAllocs"`
	Deployments   []DeploymentInfo `json:"deployments"`
	Summary       Summary          `json:"summary"`
	Partial       []string         `json:"partial"`
}

// Node is a Nomad client node.
type Node struct {
	Name          string `json:"name"`
	Datacenter    string `json:"datacenter"`
	Status        string `json:"status"` // ready, down, initializing
	Eligible      bool   `json:"eligible"`
	Drain         bool   `json:"drain"`
	Version       string `json:"version"`
	Address       string `json:"address"`
	RunningAllocs int    `json:"runningAllocs"`
}

// Job summarises one job's allocations.
type Job struct {
	Namespace string `json:"namespace"`
	ID        string `json:"id"`
	Type      string `json:"type"`   // service, batch, system, sysbatch
	Status    string `json:"status"` // running, pending, dead
	Version   uint64 `json:"version"`
	Running   int    `json:"running"`
	Starting  int    `json:"starting"`
	Queued    int    `json:"queued"`
	Failed    int    `json:"failed"`
	Lost      int    `json:"lost"`
	Complete  int    `json:"complete"`
	Healthy   bool   `json:"healthy"`
}

// Alloc is an allocation that needs attention.
type Alloc struct {
	ID       string    `json:"id"`
	Job      string    `json:"job"` // namespace/job
	Group    string    `json:"group"`
	Node     string    `json:"node"`
	Status   string    `json:"status"` // failed, lost, running (restarting)
	Restarts int       `json:"restarts"`
	Message  string    `json:"message"`
	Modified time.Time `json:"modified"`
}

// DeploymentInfo is a deployment that is running or recently failed.
type DeploymentInfo struct {
	Job         string `json:"job"`
	Version     uint64 `json:"version"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

// Summary counts the headline numbers.
type Summary struct {
	NodesReady    int `json:"nodesReady"`
	Nodes         int `json:"nodes"`
	JobsRunning   int `json:"jobsRunning"`
	Jobs          int `json:"jobs"`
	JobsDegraded  int `json:"jobsDegraded"`
	AllocsRunning int `json:"allocsRunning"`
	AllocsFailed  int `json:"allocsFailed"` // failed or lost in the snapshot window
}

const (
	problemWindow    = 24 * time.Hour
	maxProblemAllocs = 25
)

// Snapshot implements collector.Snapshotter (read-only API calls).
func (c *Collector) Snapshot(ctx context.Context) (any, error) {
	now := c.now()
	snap := Snapshot{Type: "nomad", TakenAt: now, Partial: []string{}, Nodes: []Node{}, Jobs: []Job{},
		ProblemAllocs: []Alloc{}, Deployments: []DeploymentInfo{}}
	ns := url.Values{"namespace": {"*"}}

	var (
		self struct {
			Config struct {
				Region     string `json:"Region"`
				Datacenter string `json:"Datacenter"`
				Version    struct {
					Version string `json:"Version"`
				} `json:"Version"`
			} `json:"config"`
		}
		nodes []struct {
			Name                  string `json:"Name"`
			Datacenter            string `json:"Datacenter"`
			Status                string `json:"Status"`
			SchedulingEligibility string `json:"SchedulingEligibility"`
			Drain                 bool   `json:"Drain"`
			Version               string `json:"Version"`
			Address               string `json:"Address"`
			ID                    string `json:"ID"`
		}
		jobs []struct {
			jobStub
			Type    string `json:"Type"`
			Status  string `json:"Status"`
			Version uint64 `json:"Version"`
		}
		allocs []allocStub
		deps   []deployment
	)
	type call struct {
		name     string
		path     string
		q        url.Values
		out      any
		required bool
	}
	calls := []call{
		{"agent", "/v1/agent/self", nil, &self, false},
		{"nodes", "/v1/nodes", nil, &nodes, false},
		{"jobs", "/v1/jobs", ns, &jobs, true},
		{"allocations", "/v1/allocations", ns, &allocs, false},
		{"deployments", "/v1/deployments", ns, &deps, false},
	}
	errs := make([]error, len(calls))
	var wg sync.WaitGroup
	for i, cl := range calls {
		wg.Add(1)
		go func(i int, cl call) {
			defer wg.Done()
			errs[i] = httpx.GetJSON(ctx, c.http, c.base, cl.path, cl.q, cl.out)
		}(i, cl)
	}
	wg.Wait()
	for i, cl := range calls {
		if errs[i] == nil {
			continue
		}
		if cl.required {
			return nil, fmt.Errorf("nomad %s: %w", cl.name, errs[i])
		}
		snap.Partial = append(snap.Partial, fmt.Sprintf("%s: %v", cl.name, errs[i]))
	}
	snap.Version, snap.Region, snap.Datacenter = self.Config.Version.Version, self.Config.Region, self.Config.Datacenter

	jobVersions := map[string]uint64{}
	for _, j := range jobs {
		jobVersions[j.Namespace+"/"+j.ID] = j.Version
	}

	runningPerNode := map[string]int{}
	nodeName := map[string]string{}
	for _, n := range nodes {
		nodeName[n.ID] = n.Name
	}
	for _, a := range allocs {
		key := a.Namespace + "/" + a.JobID
		if a.ClientStatus == "running" {
			runningPerNode[a.NodeID]++
			snap.Summary.AllocsRunning++
		}
		modified := nsToTime(a.ModifyTime)
		restarts, msg := 0, ""
		for _, ts := range a.TaskStates {
			for _, e := range ts.Events {
				if e.Type == "Restarting" {
					restarts++
				}
				if e.Type == "Terminated" || e.Type == "Driver Failure" || e.Type == "Not Restarting" {
					msg = e.DisplayMessage
				}
			}
		}
		problem := a.ClientStatus == "failed" || a.ClientStatus == "lost" || (a.ClientStatus == "running" && restarts > 0)
		if !problem || now.Sub(modified) > problemWindow {
			continue
		}
		// Ignore allocations of older job versions that have been replaced.
		if a.ClientStatus != "running" && a.JobVersion < jobVersions[key] {
			continue
		}
		if a.ClientStatus != "running" {
			snap.Summary.AllocsFailed++
		}
		snap.ProblemAllocs = append(snap.ProblemAllocs, Alloc{ID: short(a.ID), Job: key, Group: a.TaskGroup,
			Node: nodeName[a.NodeID], Status: a.ClientStatus, Restarts: restarts, Message: truncate(msg, 200), Modified: modified})
	}
	sort.Slice(snap.ProblemAllocs, func(i, j int) bool { return snap.ProblemAllocs[i].Modified.After(snap.ProblemAllocs[j].Modified) })
	if len(snap.ProblemAllocs) > maxProblemAllocs {
		snap.ProblemAllocs = snap.ProblemAllocs[:maxProblemAllocs]
	}

	for _, n := range nodes {
		snap.Summary.Nodes++
		if n.Status == "ready" {
			snap.Summary.NodesReady++
		}
		snap.Nodes = append(snap.Nodes, Node{Name: n.Name, Datacenter: n.Datacenter, Status: n.Status,
			Eligible: n.SchedulingEligibility == "eligible", Drain: n.Drain, Version: n.Version, Address: n.Address,
			RunningAllocs: runningPerNode[n.ID]})
	}
	sort.Slice(snap.Nodes, func(i, j int) bool { return snap.Nodes[i].Name < snap.Nodes[j].Name })

	for _, j := range jobs {
		jb := Job{Namespace: j.Namespace, ID: j.ID, Type: j.Type, Status: j.Status, Version: j.Version}
		for _, g := range j.JobSummary.Summary {
			jb.Running += g.Running
			jb.Starting += g.Starting
			jb.Queued += g.Queued
			jb.Failed += g.Failed
			jb.Lost += g.Lost
			jb.Complete += g.Complete
		}
		// A job is healthy when nothing is waiting to be placed and, for
		// long-running jobs, something is actually running.
		jb.Healthy = jb.Queued == 0 && (jb.Status != "running" || jb.Type == "batch" || jb.Type == "sysbatch" || jb.Running > 0)
		if jb.Status == "dead" && (jb.Type == "service" || jb.Type == "system") && jb.Complete == 0 {
			jb.Healthy = false
		}
		snap.Summary.Jobs++
		if jb.Status == "running" {
			snap.Summary.JobsRunning++
		}
		if !jb.Healthy {
			snap.Summary.JobsDegraded++
		}
		snap.Jobs = append(snap.Jobs, jb)
	}
	sort.SliceStable(snap.Jobs, func(i, j int) bool {
		a, b := snap.Jobs[i], snap.Jobs[j]
		if a.Healthy != b.Healthy {
			return !a.Healthy
		}
		return a.Namespace+"/"+a.ID < b.Namespace+"/"+b.ID
	})

	for _, d := range deps {
		if d.Status != "running" && d.Status != "failed" && d.Status != "paused" {
			continue
		}
		if d.Status == "failed" && uint64(d.JobVersion) < jobVersions[d.Namespace+"/"+d.JobID] {
			continue // superseded by a newer version
		}
		snap.Deployments = append(snap.Deployments, DeploymentInfo{Job: d.Namespace + "/" + d.JobID, Version: uint64(d.JobVersion),
			Status: d.Status, Description: d.StatusDescription})
	}
	return snap, nil
}
