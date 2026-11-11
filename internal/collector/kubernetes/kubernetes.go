// Package kubernetes collects pod, event, rollout and memory signals from a
// Kubernetes cluster (including k3s) using kubectl, so any kubeconfig auth
// method kubectl supports works without extra dependencies.
package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"PhantomOps/internal/model"
)

const (
	memWarnRatio     = 0.80
	memCriticalRatio = 0.95
)

// Runner executes kubectl with the given arguments and returns stdout.
// It is a seam for tests.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Collector implements collector.Collector for Kubernetes.
type Collector struct {
	run Runner
	now func() time.Time
}

// New creates a collector. kubeContext may be empty to use the current
// context; namespace may be empty for all namespaces.
func New(kubeContext string) *Collector {
	return NewWithRunner(kubectlRunner(kubeContext))
}

// NewWithRunner creates a collector with a custom kubectl runner.
func NewWithRunner(r Runner) *Collector {
	return &Collector{run: r, now: func() time.Time { return time.Now().UTC() }}
}

func kubectlRunner(kubeContext string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if kubeContext != "" {
			args = append([]string{"--context", kubeContext}, args...)
		}
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
		}
		return out.Bytes(), nil
	}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "kubernetes" }

// Collect implements collector.Collector.
func (c *Collector) Collect(ctx context.Context, since time.Time) ([]model.Event, error) {
	var pods podList
	if err := c.getJSON(ctx, &pods, "get", "pods", "-A", "-o", "json"); err != nil {
		return nil, err
	}
	var events []model.Event
	events = append(events, c.podEvents(pods, since)...)

	// The remaining sources are best-effort: a missing metrics-server or
	// RBAC restriction must not hide pod problems.
	var warn []string
	var evs eventList
	if err := c.getJSON(ctx, &evs, "get", "events", "-A", "-o", "json"); err == nil {
		events = append(events, c.warningEvents(evs, since)...)
	} else {
		warn = append(warn, "events: "+err.Error())
	}
	var deps deploymentList
	if err := c.getJSON(ctx, &deps, "get", "deployments", "-A", "-o", "json"); err == nil {
		events = append(events, c.deploymentEvents(deps, since)...)
	} else {
		warn = append(warn, "deployments: "+err.Error())
	}
	var pm podMetricsList
	if err := c.getJSON(ctx, &pm, "get", "--raw", "/apis/metrics.k8s.io/v1beta1/pods"); err == nil {
		events = append(events, c.memoryEvents(pods, pm)...)
	} else {
		warn = append(warn, "metrics: "+err.Error())
	}
	for _, w := range warn {
		events = append(events, model.Event{Time: c.now(), Source: model.SourceKubernetes, Kind: model.KindOther,
			Severity: model.SeverityInfo, Subject: "collector", Title: "Partial data", Detail: w})
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	return events, nil
}

func (c *Collector) getJSON(ctx context.Context, out any, args ...string) error {
	b, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode kubectl %s: %w", args[1], err)
	}
	return nil
}

// failing waiting reasons that indicate a broken pod.
var badWaiting = map[string]bool{
	"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true,
	"CreateContainerConfigError": true, "CreateContainerError": true, "InvalidImageName": true,
}

func (c *Collector) podEvents(pods podList, since time.Time) []model.Event {
	var out []model.Event
	for _, p := range pods.Items {
		if p.Status.Phase == "Succeeded" {
			continue
		}
		subject := p.Metadata.Namespace + "/" + p.Metadata.Name
		add := func(t time.Time, kind model.Kind, sev model.Severity, title, detail string) {
			out = append(out, model.Event{Time: t, Source: model.SourceKubernetes, Kind: kind,
				Severity: sev, Subject: subject, Title: title, Detail: detail})
		}

		if start := parseTime(p.Status.StartTime); start.After(since) {
			add(start, model.KindDeployment, model.SeverityInfo, "Pod started", subject+" was scheduled and started.")
		}
		if p.Status.Phase == "Failed" {
			add(c.now(), model.KindDegraded, model.SeverityCritical, "Pod failed",
				strings.TrimSpace(p.Status.Reason+" "+p.Status.Message))
		}
		for _, cs := range p.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil && badWaiting[w.Reason] {
				add(c.now(), model.KindDegraded, model.SeverityCritical, w.Reason,
					fmt.Sprintf("Container %s in %s: %s (restarts: %d).", cs.Name, subject, orDefault(w.Message, w.Reason), cs.RestartCount))
			}
			if t := cs.LastState.Terminated; t != nil {
				if fin := parseTime(t.FinishedAt); fin.After(since) {
					if t.Reason == "OOMKilled" {
						add(fin, model.KindResource, model.SeverityCritical, "Container OOM-killed",
							fmt.Sprintf("Container %s in %s exceeded its memory limit (restarts: %d).", cs.Name, subject, cs.RestartCount))
					} else if t.ExitCode != 0 {
						add(fin, model.KindDegraded, model.SeverityWarning, "Container terminated",
							fmt.Sprintf("Container %s in %s exited with code %d (%s).", cs.Name, subject, t.ExitCode, t.Reason))
					}
				}
			}
		}
	}
	return out
}

func (c *Collector) warningEvents(list eventList, since time.Time) []model.Event {
	var out []model.Event
	for _, e := range list.Items {
		if e.Type != "Warning" {
			continue
		}
		t := parseTime(e.LastTimestamp)
		if t.IsZero() {
			t = parseTime(e.EventTime)
		}
		if t.IsZero() {
			t = parseTime(e.Metadata.CreationTimestamp)
		}
		if !t.After(since) {
			continue
		}
		title := e.Reason
		if e.Count > 1 {
			title = fmt.Sprintf("%s (x%d)", e.Reason, e.Count)
		}
		out = append(out, model.Event{
			Time: t, Source: model.SourceKubernetes, Kind: model.KindDegraded, Severity: model.SeverityWarning,
			Subject: e.Metadata.Namespace + "/" + e.InvolvedObject.Name, Title: title, Detail: e.Message,
		})
	}
	return out
}

func (c *Collector) deploymentEvents(list deploymentList, since time.Time) []model.Event {
	var out []model.Event
	for _, d := range list.Items {
		subject := d.Metadata.Namespace + "/" + d.Metadata.Name
		for _, cond := range d.Status.Conditions {
			t := parseTime(cond.LastUpdateTime)
			switch {
			case cond.Type == "Progressing" && cond.Status == "False":
				// ProgressDeadlineExceeded: a rollout is stuck.
				out = append(out, model.Event{Time: orNow(t, c.now()), Source: model.SourceKubernetes, Kind: model.KindDeployment,
					Severity: model.SeverityCritical, Subject: subject, Title: "Rollout stuck",
					Detail: fmt.Sprintf("%s: %s", cond.Reason, cond.Message)})
			case cond.Type == "Progressing" && t.After(since) &&
				(cond.Reason == "ReplicaSetUpdated" || cond.Reason == "NewReplicaSetAvailable"):
				out = append(out, model.Event{Time: t, Source: model.SourceKubernetes, Kind: model.KindDeployment,
					Severity: model.SeverityInfo, Subject: subject, Title: "Deployment rollout",
					Detail: cond.Message})
			}
		}
		if u := d.Status.UnavailableReplicas; u > 0 {
			out = append(out, model.Event{Time: c.now(), Source: model.SourceKubernetes, Kind: model.KindDegraded,
				Severity: model.SeverityWarning, Subject: subject, Title: "Replicas unavailable",
				Detail: fmt.Sprintf("%d of %d replicas are unavailable.", u, d.Spec.Replicas)})
		}
	}
	return out
}

func (c *Collector) memoryEvents(pods podList, pm podMetricsList) []model.Event {
	limits := map[string]uint64{} // ns/pod/container -> bytes
	for _, p := range pods.Items {
		for _, ct := range p.Spec.Containers {
			if v, ok := ct.Resources.Limits["memory"]; ok {
				if b, err := parseQuantity(v); err == nil {
					limits[p.Metadata.Namespace+"/"+p.Metadata.Name+"/"+ct.Name] = b
				}
			}
		}
	}
	var out []model.Event
	for _, m := range pm.Items {
		for _, ct := range m.Containers {
			key := m.Metadata.Namespace + "/" + m.Metadata.Name + "/" + ct.Name
			lim, ok := limits[key]
			if !ok || lim == 0 {
				continue
			}
			use, err := parseQuantity(ct.Usage.Memory)
			if err != nil {
				continue
			}
			ratio := float64(use) / float64(lim)
			if ratio < memWarnRatio {
				continue
			}
			sev := model.SeverityWarning
			if ratio >= memCriticalRatio {
				sev = model.SeverityCritical
			}
			out = append(out, model.Event{Time: c.now(), Source: model.SourceKubernetes, Kind: model.KindResource,
				Severity: sev, Subject: m.Metadata.Namespace + "/" + m.Metadata.Name, Title: "High memory usage",
				Detail: fmt.Sprintf("Container %s is using %d MB of %d MB (%.0f%%).", ct.Name, use>>20, lim>>20, ratio*100)})
		}
	}
	return out
}

// parseQuantity parses Kubernetes memory quantities (e.g. 128Mi, 1G, 1500000).
func parseQuantity(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   float64
	}{
		{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
		{"k", 1e3}, {"K", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
	}
	mult := 1.0
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSuffix(s, u.suffix), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid quantity %q", s)
	}
	return uint64(f * mult), nil
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func orNow(t, now time.Time) time.Time {
	if t.IsZero() {
		return now
	}
	return t
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
