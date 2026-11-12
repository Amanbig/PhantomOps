package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Snapshot is the current state of a Docker engine.
type Snapshot struct {
	Type       string      `json:"type"` // "docker"
	TakenAt    time.Time   `json:"takenAt"`
	Engine     Engine      `json:"engine"`
	Containers []Container `json:"containers"`
	Summary    Summary     `json:"summary"`
}

// Engine describes the Docker host.
type Engine struct {
	Version     string `json:"version"`
	OS          string `json:"os"`
	Kernel      string `json:"kernel"`
	CPUs        int    `json:"cpus"`
	MemoryBytes int64  `json:"memoryBytes"`
	Images      int    `json:"images"`
}

// Summary counts containers by condition.
type Summary struct {
	Total     int `json:"total"`
	Running   int `json:"running"`
	Stopped   int `json:"stopped"`
	Unhealthy int `json:"unhealthy"`
	Restarts  int `json:"restarts"`
}

// Container is one container. CPU and memory are only set for running
// containers whose stats could be read.
type Container struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Image       string    `json:"image"`
	State       string    `json:"state"`  // running, exited, restarting, paused, ...
	Status      string    `json:"status"` // human text from Docker, e.g. "Up 3 hours"
	Health      string    `json:"health"` // healthy, unhealthy, starting or ""
	ExitCode    int       `json:"exitCode"`
	OOMKilled   bool      `json:"oomKilled"`
	Restarts    int       `json:"restarts"`
	StartedAt   time.Time `json:"startedAt"`
	CPUPercent  *float64  `json:"cpuPercent,omitempty"`
	MemoryBytes *uint64   `json:"memoryBytes,omitempty"`
	MemoryLimit *uint64   `json:"memoryLimit,omitempty"` // nil when unlimited
	Ports       []string  `json:"ports"`
	Compose     string    `json:"compose"` // compose project, if any
}

type listEntry struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
	Ports  []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
}

type fullStats struct {
	CPUStats    cpuStats `json:"cpu_stats"`
	PreCPUStats cpuStats `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			InactiveFile uint64 `json:"inactive_file"`
		} `json:"stats"`
	} `json:"memory_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs  int    `json:"online_cpus"`
}

// statsConcurrency bounds parallel stats calls (each takes ~1 s because
// Docker samples CPU twice).
const statsConcurrency = 8

// Snapshot implements collector.Snapshotter.
func (c *Collector) Snapshot(ctx context.Context) (any, error) {
	snap := Snapshot{Type: "docker", TakenAt: c.now()}

	var info struct {
		ServerVersion   string `json:"ServerVersion"`
		OperatingSystem string `json:"OperatingSystem"`
		KernelVersion   string `json:"KernelVersion"`
		NCPU            int    `json:"NCPU"`
		MemTotal        int64  `json:"MemTotal"`
		Images          int    `json:"Images"`
	}
	if err := c.api.getJSON(ctx, "/info", nil, &info); err != nil {
		return nil, fmt.Errorf("docker info: %w", err)
	}
	snap.Engine = Engine{Version: info.ServerVersion, OS: info.OperatingSystem, Kernel: info.KernelVersion,
		CPUs: info.NCPU, MemoryBytes: info.MemTotal, Images: info.Images}

	var list []listEntry
	if err := c.api.getJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &list); err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	snap.Containers = make([]Container, len(list))
	var wg sync.WaitGroup
	sem := make(chan struct{}, statsConcurrency)
	for i, e := range list {
		ct := Container{ID: shortID(e.ID), Name: strings.TrimPrefix(firstName(containerSummary{ID: e.ID, Names: e.Names}), "/"),
			Image: e.Image, State: e.State, Status: e.Status, Compose: e.Labels["com.docker.compose.project"], Ports: ports(e)}
		snap.Containers[i] = ct
		wg.Add(1)
		go func(i int, id string, running bool) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c.fillDetails(ctx, &snap.Containers[i], id, running)
		}(i, e.ID, e.State == "running")
	}
	wg.Wait()

	sort.SliceStable(snap.Containers, func(i, j int) bool {
		a, b := snap.Containers[i], snap.Containers[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		return a.Name < b.Name
	})
	for i := range snap.Containers {
		// Without a limit, Docker reports the host's total memory as the
		// limit; treat that as unlimited so no misleading "% of limit" shows.
		if l := snap.Containers[i].MemoryLimit; l != nil && info.MemTotal > 0 && *l >= uint64(info.MemTotal) {
			snap.Containers[i].MemoryLimit = nil
		}
	}
	for _, ct := range snap.Containers {
		snap.Summary.Total++
		snap.Summary.Restarts += ct.Restarts
		if ct.State == "running" {
			snap.Summary.Running++
		} else {
			snap.Summary.Stopped++
		}
		if ct.Health == "unhealthy" && ct.State == "running" {
			snap.Summary.Unhealthy++
		}
	}
	return snap, nil
}

// rank orders containers: problems first, then running, then stopped.
func rank(c Container) int {
	switch {
	case c.State == "running" && c.Health == "unhealthy", c.State == "restarting":
		return 0
	case c.State == "running":
		return 1
	}
	return 2
}

func (c *Collector) fillDetails(ctx context.Context, ct *Container, id string, running bool) {
	var in containerInspect
	if err := c.api.getJSON(ctx, "/containers/"+id+"/json", nil, &in); err == nil {
		ct.Restarts, ct.ExitCode, ct.OOMKilled = in.RestartCount, in.State.ExitCode, in.State.OOMKilled
		ct.StartedAt = parseTime(in.State.StartedAt)
		// Health is only meaningful while running (stopped containers keep a stale value).
		if running && in.State.Health != nil {
			ct.Health = in.State.Health.Status
		}
	}
	if !running {
		return
	}
	// stream=false (without one-shot) makes Docker take two samples, so CPU %
	// can be computed.
	raw, err := c.api.get(ctx, "/containers/"+id+"/stats", url.Values{"stream": {"false"}})
	if err != nil {
		return
	}
	var st fullStats
	if json.Unmarshal(raw, &st) != nil {
		return
	}
	ws := containerStats{}
	ws.MemoryStats.Usage, ws.MemoryStats.Stats.InactiveFile = st.MemoryStats.Usage, st.MemoryStats.Stats.InactiveFile
	mem := ws.workingSet()
	ct.MemoryBytes = &mem
	if lim := st.MemoryStats.Limit; lim > 0 && lim < 1<<60 {
		ct.MemoryLimit = &lim
	}
	cpuDelta := float64(st.CPUStats.CPUUsage.TotalUsage) - float64(st.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(st.CPUStats.SystemUsage) - float64(st.PreCPUStats.SystemUsage)
	if cpuDelta >= 0 && sysDelta > 0 {
		cpus := st.CPUStats.OnlineCPUs
		if cpus == 0 {
			cpus = 1
		}
		pct := cpuDelta / sysDelta * float64(cpus) * 100
		ct.CPUPercent = &pct
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func ports(e listEntry) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range e.Ports {
		s := fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		if p.PublicPort > 0 {
			s = fmt.Sprintf("%d→%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
