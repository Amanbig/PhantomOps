package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Snapshot is the current state of a Kubernetes cluster.
type Snapshot struct {
	Type          string       `json:"type"` // "kubernetes"
	TakenAt       time.Time    `json:"takenAt"`
	ServerVersion string       `json:"serverVersion"`
	Nodes         []Node       `json:"nodes"`
	Deployments   []Deployment `json:"deployments"`
	ProblemPods   []PodInfo    `json:"problemPods"`
	Warnings      []Warning    `json:"warnings"`
	Pods          PodCounts    `json:"pods"`
	Namespaces    int          `json:"namespaces"`
	// MetricsAvailable is false when metrics-server is not installed, in
	// which case node usage is unknown.
	MetricsAvailable bool `json:"metricsAvailable"`
	// Partial lists parts that could not be read (e.g. RBAC denied).
	Partial []string `json:"partial"`
}

// Node is one cluster node. Usage fields are nil without metrics-server.
type Node struct {
	Name          string   `json:"name"`
	Ready         bool     `json:"ready"`
	Roles         []string `json:"roles"`
	KubeletVer    string   `json:"kubeletVersion"`
	CPUMillis     int64    `json:"cpuMillis"`   // allocatable
	MemoryBytes   uint64   `json:"memoryBytes"` // allocatable
	CPUUsedMillis *int64   `json:"cpuUsedMillis,omitempty"`
	MemoryUsed    *uint64  `json:"memoryUsed,omitempty"`
	Pods          int      `json:"pods"`
}

// Deployment is one Deployment's rollout state.
type Deployment struct {
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Desired   int       `json:"desired"`
	Ready     int       `json:"ready"`
	Updated   int       `json:"updated"`
	Available int       `json:"available"`
	Images    []string  `json:"images"`
	Created   time.Time `json:"created"`
	Healthy   bool      `json:"healthy"`
}

// PodInfo describes a pod that needs attention.
type PodInfo struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Reason    string `json:"reason"` // e.g. CrashLoopBackOff, ImagePullBackOff, OOMKilled, NotReady
	Restarts  int    `json:"restarts"`
	Node      string `json:"node"`
	Age       string `json:"age"`
}

// PodCounts counts pods by phase.
type PodCounts struct {
	Total     int `json:"total"`
	Running   int `json:"running"`
	Pending   int `json:"pending"`
	Failed    int `json:"failed"`
	Succeeded int `json:"succeeded"`
	NotReady  int `json:"notReady"` // running but not all containers ready
}

// Warning is a recent Warning event.
type Warning struct {
	Time    time.Time `json:"time"`
	Object  string    `json:"object"` // namespace/kind/name
	Reason  string    `json:"reason"`
	Message string    `json:"message"`
	Count   int       `json:"count"`
}

const (
	warningWindow  = time.Hour
	maxWarnings    = 15
	maxProblemPods = 25
)

type snapNode struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Allocatable map[string]string `json:"allocatable"`
		Conditions  []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
		NodeInfo struct {
			KubeletVersion string `json:"kubeletVersion"`
		} `json:"nodeInfo"`
	} `json:"status"`
}

type snapDeployment struct {
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		Replicas *int `json:"replicas"`
		Template struct {
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas     int `json:"readyReplicas"`
		UpdatedReplicas   int `json:"updatedReplicas"`
		AvailableReplicas int `json:"availableReplicas"`
	} `json:"status"`
}

type snapPod struct {
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase             string            `json:"phase"`
		Reason            string            `json:"reason"`
		ContainerStatuses []containerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type nodeMetrics struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Usage struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"usage"`
	} `json:"items"`
}

// Snapshot implements collector.Snapshotter. Only read-only kubectl
// commands are used.
func (c *Collector) Snapshot(ctx context.Context) (any, error) {
	now := c.now()
	snap := Snapshot{Type: "kubernetes", TakenAt: now, Partial: []string{}}

	var (
		nodes   struct{ Items []snapNode }
		deps    struct{ Items []snapDeployment }
		pods    struct{ Items []snapPod }
		events  eventList
		metrics nodeMetrics
		version struct {
			ServerVersion struct {
				GitVersion string `json:"gitVersion"`
			} `json:"serverVersion"`
		}
	)
	type job struct {
		name     string
		out      any
		args     []string
		required bool
	}
	jobs := []job{
		{"nodes", &nodes, []string{"get", "nodes", "-o", "json"}, true},
		{"deployments", &deps, []string{"get", "deployments", "-A", "-o", "json"}, false},
		{"pods", &pods, []string{"get", "pods", "-A", "-o", "json"}, false},
		{"events", &events, []string{"get", "events", "-A", "--field-selector", "type=Warning", "-o", "json"}, false},
		{"metrics", &metrics, []string{"get", "--raw", "/apis/metrics.k8s.io/v1beta1/nodes"}, false},
		{"version", &version, []string{"version", "-o", "json"}, false},
	}
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			errs[i] = c.getJSON(ctx, j.out, j.args...)
		}(i, j)
	}
	wg.Wait()
	for i, j := range jobs {
		if errs[i] == nil {
			continue
		}
		if j.required {
			return nil, errs[i]
		}
		if j.name == "metrics" {
			continue // reported via MetricsAvailable
		}
		snap.Partial = append(snap.Partial, fmt.Sprintf("%s: %v", j.name, errs[i]))
	}
	snap.ServerVersion = version.ServerVersion.GitVersion
	snap.MetricsAvailable = errs[4] == nil

	usage := map[string][2]string{}
	for _, m := range metrics.Items {
		usage[m.Metadata.Name] = [2]string{m.Usage.CPU, m.Usage.Memory}
	}
	podsPerNode := map[string]int{}
	namespaces := map[string]bool{}

	// pods
	for _, p := range pods.Items {
		namespaces[p.Metadata.Namespace] = true
		podsPerNode[p.Spec.NodeName]++
		snap.Pods.Total++
		restarts, ready, total := 0, 0, len(p.Status.ContainerStatuses)
		reason := p.Status.Reason
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
			if cs.Ready {
				ready++
			}
			if w := cs.State.Waiting; w != nil && w.Reason != "" && w.Reason != "ContainerCreating" {
				reason = w.Reason
			} else if reason == "" && cs.LastState.Terminated != nil && cs.LastState.Terminated.Reason == "OOMKilled" {
				reason = "OOMKilled"
			}
		}
		switch p.Status.Phase {
		case "Running":
			snap.Pods.Running++
			if ready < total {
				snap.Pods.NotReady++
				if reason == "" {
					reason = "NotReady"
				}
			}
		case "Pending":
			snap.Pods.Pending++
		case "Failed":
			snap.Pods.Failed++
		case "Succeeded":
			snap.Pods.Succeeded++
			continue
		}
		if reason != "" || p.Status.Phase == "Pending" || p.Status.Phase == "Failed" {
			snap.ProblemPods = append(snap.ProblemPods, PodInfo{Namespace: p.Metadata.Namespace, Name: p.Metadata.Name,
				Phase: p.Status.Phase, Reason: reason, Restarts: restarts, Node: p.Spec.NodeName,
				Age: age(now, parseTime(p.Metadata.CreationTimestamp))})
		}
	}
	sort.SliceStable(snap.ProblemPods, func(i, j int) bool { return snap.ProblemPods[i].Restarts > snap.ProblemPods[j].Restarts })
	if len(snap.ProblemPods) > maxProblemPods {
		snap.ProblemPods = snap.ProblemPods[:maxProblemPods]
	}

	// nodes
	for _, n := range nodes.Items {
		nd := Node{Name: n.Metadata.Name, KubeletVer: n.Status.NodeInfo.KubeletVersion, Pods: podsPerNode[n.Metadata.Name]}
		for _, cnd := range n.Status.Conditions {
			if cnd.Type == "Ready" {
				nd.Ready = cnd.Status == "True"
			}
		}
		for l := range n.Metadata.Labels {
			if r, ok := strings.CutPrefix(l, "node-role.kubernetes.io/"); ok && r != "" {
				nd.Roles = append(nd.Roles, r)
			}
		}
		sort.Strings(nd.Roles)
		nd.CPUMillis = parseCPUMillis(n.Status.Allocatable["cpu"])
		nd.MemoryBytes, _ = parseQuantity(n.Status.Allocatable["memory"])
		if u, ok := usage[n.Metadata.Name]; ok {
			cpu := parseCPUMillis(u[0])
			mem, _ := parseQuantity(u[1])
			nd.CPUUsedMillis, nd.MemoryUsed = &cpu, &mem
		}
		snap.Nodes = append(snap.Nodes, nd)
	}
	sort.Slice(snap.Nodes, func(i, j int) bool { return snap.Nodes[i].Name < snap.Nodes[j].Name })

	// deployments: unhealthy first
	for _, d := range deps.Items {
		namespaces[d.Metadata.Namespace] = true
		desired := 1
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		dep := Deployment{Namespace: d.Metadata.Namespace, Name: d.Metadata.Name, Desired: desired,
			Ready: d.Status.ReadyReplicas, Updated: d.Status.UpdatedReplicas, Available: d.Status.AvailableReplicas,
			Created: parseTime(d.Metadata.CreationTimestamp)}
		for _, ct := range d.Spec.Template.Spec.Containers {
			dep.Images = append(dep.Images, ct.Image)
		}
		dep.Healthy = dep.Ready >= dep.Desired && dep.Available >= dep.Desired
		snap.Deployments = append(snap.Deployments, dep)
	}
	sort.SliceStable(snap.Deployments, func(i, j int) bool {
		a, b := snap.Deployments[i], snap.Deployments[j]
		if a.Healthy != b.Healthy {
			return !a.Healthy
		}
		return a.Namespace+"/"+a.Name < b.Namespace+"/"+b.Name
	})

	// recent warnings, newest first
	for _, e := range events.Items {
		t := parseTime(e.LastTimestamp)
		if t.IsZero() {
			t = parseTime(e.EventTime)
		}
		if t.IsZero() {
			t = parseTime(e.Metadata.CreationTimestamp)
		}
		if now.Sub(t) > warningWindow {
			continue
		}
		snap.Warnings = append(snap.Warnings, Warning{Time: t, Reason: e.Reason, Message: e.Message, Count: max(e.Count, 1),
			Object: e.Metadata.Namespace + "/" + strings.ToLower(e.InvolvedObject.Kind) + "/" + e.InvolvedObject.Name})
	}
	sort.Slice(snap.Warnings, func(i, j int) bool { return snap.Warnings[i].Time.After(snap.Warnings[j].Time) })
	if len(snap.Warnings) > maxWarnings {
		snap.Warnings = snap.Warnings[:maxWarnings]
	}
	snap.Namespaces = len(namespaces)
	if snap.Nodes == nil {
		snap.Nodes = []Node{}
	}
	if snap.Deployments == nil {
		snap.Deployments = []Deployment{}
	}
	if snap.ProblemPods == nil {
		snap.ProblemPods = []PodInfo{}
	}
	if snap.Warnings == nil {
		snap.Warnings = []Warning{}
	}
	return snap, nil
}

// parseCPUMillis parses CPU quantities ("2", "250m", "123456789n", "500u").
func parseCPUMillis(s string) int64 {
	s = strings.TrimSpace(s)
	div, mul := 1.0, 1000.0
	switch {
	case strings.HasSuffix(s, "n"):
		s, mul, div = strings.TrimSuffix(s, "n"), 1, 1e6
	case strings.HasSuffix(s, "u"):
		s, mul, div = strings.TrimSuffix(s, "u"), 1, 1e3
	case strings.HasSuffix(s, "m"):
		s, mul = strings.TrimSuffix(s, "m"), 1
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f * mul / div)
}

func age(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
