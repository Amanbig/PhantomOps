// Package nomad collects job, allocation and task-event signals from a
// HashiCorp Nomad cluster via its HTTP API.
package nomad

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"PhantomOps/internal/httpx"
	"PhantomOps/internal/model"
)

// Collector implements collector.Collector for Nomad.
type Collector struct {
	base string
	http *http.Client
	now  func() time.Time
}

// New creates a collector for the Nomad API at baseURL. token may be empty
// when ACLs are disabled.
func New(baseURL, token string) *Collector {
	hc := httpx.NewClient()
	if token != "" {
		hc.Transport = tokenTransport{token: token, next: http.DefaultTransport}
	}
	return &Collector{base: baseURL, http: hc, now: func() time.Time { return time.Now().UTC() }}
}

type tokenTransport struct {
	token string
	next  http.RoundTripper
}

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Nomad-Token", t.token)
	return t.next.RoundTrip(r)
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "nomad" }

type taskEvent struct {
	Type           string            `json:"Type"`
	Time           int64             `json:"Time"`
	DisplayMessage string            `json:"DisplayMessage"`
	Details        map[string]string `json:"Details"`
}

type taskState struct {
	State  string      `json:"State"`
	Failed bool        `json:"Failed"`
	Events []taskEvent `json:"Events"`
}

type allocStub struct {
	ID           string               `json:"ID"`
	JobID        string               `json:"JobID"`
	Namespace    string               `json:"Namespace"`
	TaskGroup    string               `json:"TaskGroup"`
	ClientStatus string               `json:"ClientStatus"`
	ModifyTime   int64                `json:"ModifyTime"`
	TaskStates   map[string]taskState `json:"TaskStates"`
}

type groupSummary struct {
	Queued int `json:"Queued"`
	Lost   int `json:"Lost"`
}

type jobStub struct {
	ID         string `json:"ID"`
	Namespace  string `json:"Namespace"`
	SubmitTime int64  `json:"SubmitTime"`
	JobSummary struct {
		Summary map[string]groupSummary `json:"Summary"`
	} `json:"JobSummary"`
}

type deployment struct {
	JobID             string `json:"JobID"`
	Namespace         string `json:"Namespace"`
	JobVersion        int    `json:"JobVersion"`
	Status            string `json:"Status"`
	StatusDescription string `json:"StatusDescription"`
}

// Collect implements collector.Collector.
func (c *Collector) Collect(ctx context.Context, since time.Time) ([]model.Event, error) {
	ns := url.Values{"namespace": {"*"}}

	var allocs []allocStub
	if err := httpx.GetJSON(ctx, c.http, c.base, "/v1/allocations", ns, &allocs); err != nil {
		return nil, fmt.Errorf("nomad allocations: %w", err)
	}
	var events []model.Event
	events = append(events, c.allocEvents(allocs, since)...)

	// Jobs and deployments are best-effort.
	submit := map[string]time.Time{}
	var jobs []jobStub
	if err := httpx.GetJSON(ctx, c.http, c.base, "/v1/jobs", ns, &jobs); err == nil {
		for _, j := range jobs {
			submit[j.Namespace+"/"+j.ID] = nsToTime(j.SubmitTime)
		}
		events = append(events, c.jobEvents(jobs, since)...)
	} else {
		events = append(events, c.partial("jobs", err))
	}
	var deps []deployment
	if err := httpx.GetJSON(ctx, c.http, c.base, "/v1/deployments", ns, &deps); err == nil {
		events = append(events, c.deploymentEvents(deps, submit, since)...)
	} else {
		events = append(events, c.partial("deployments", err))
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	return events, nil
}

func (c *Collector) partial(what string, err error) model.Event {
	return model.Event{Time: c.now(), Source: model.SourceNomad, Kind: model.KindOther,
		Severity: model.SeverityInfo, Subject: "collector", Title: "Partial data",
		Detail: fmt.Sprintf("%s: %v", what, err)}
}

func (c *Collector) allocEvents(allocs []allocStub, since time.Time) []model.Event {
	var out []model.Event
	for _, a := range allocs {
		base := a.Namespace + "/" + a.JobID
		if a.ClientStatus == "lost" {
			if t := nsToTime(a.ModifyTime); t.After(since) {
				out = append(out, model.Event{Time: t, Source: model.SourceNomad, Kind: model.KindDegraded,
					Severity: model.SeverityCritical, Subject: base, Title: "Allocation lost",
					Detail: fmt.Sprintf("Allocation %s of %s/%s was lost (node down or unreachable).", short(a.ID), a.JobID, a.TaskGroup)})
			}
		}
		for task, ts := range a.TaskStates {
			subject := fmt.Sprintf("%s/%s/%s", base, a.TaskGroup, task)
			for _, e := range ts.Events {
				t := nsToTime(e.Time)
				if !t.After(since) {
					continue
				}
				if ev, ok := c.mapTaskEvent(e, t, subject); ok {
					out = append(out, ev)
				}
			}
		}
	}
	return out
}

// mapTaskEvent converts interesting Nomad task events; routine lifecycle
// events (Received, Task Setup, Driver, ...) are ignored.
func (c *Collector) mapTaskEvent(e taskEvent, t time.Time, subject string) (model.Event, bool) {
	ev := model.Event{Time: t, Source: model.SourceNomad, Subject: subject}
	switch e.Type {
	case "Started":
		ev.Kind, ev.Severity, ev.Title = model.KindDeployment, model.SeverityInfo, "Task started"
		ev.Detail = subject + " started."
	case "Terminated":
		switch {
		case e.Details["oom_killed"] == "true":
			ev.Kind, ev.Severity, ev.Title = model.KindResource, model.SeverityCritical, "Task OOM-killed"
			ev.Detail = fmt.Sprintf("%s exceeded its memory limit (exit code %s).", subject, e.Details["exit_code"])
		case e.Details["exit_code"] != "" && e.Details["exit_code"] != "0":
			ev.Kind, ev.Severity, ev.Title = model.KindDegraded, model.SeverityWarning, "Task exited"
			ev.Detail = fmt.Sprintf("%s: %s", subject, e.DisplayMessage)
		default:
			return model.Event{}, false
		}
	case "Driver Failure", "Setup Failure", "Failed Validation":
		ev.Kind, ev.Severity, ev.Title = model.KindDegraded, model.SeverityCritical, e.Type
		ev.Detail = truncate(orDefault(e.Details["driver_error"], e.DisplayMessage), 300)
	case "Restarting":
		ev.Kind, ev.Severity, ev.Title = model.KindDegraded, model.SeverityWarning, "Task restarting"
		ev.Detail = fmt.Sprintf("%s: %s", subject, e.DisplayMessage)
	case "Not Restarting":
		ev.Kind, ev.Severity, ev.Title = model.KindDegraded, model.SeverityCritical, "Task gave up restarting"
		ev.Detail = fmt.Sprintf("%s: %s", subject, e.DisplayMessage)
	case "Alloc Unhealthy":
		ev.Kind, ev.Severity, ev.Title = model.KindDegraded, model.SeverityCritical, "Allocation unhealthy"
		ev.Detail = truncate(e.DisplayMessage, 300)
	default:
		return model.Event{}, false
	}
	return ev, true
}

func (c *Collector) jobEvents(jobs []jobStub, since time.Time) []model.Event {
	var out []model.Event
	for _, j := range jobs {
		subject := j.Namespace + "/" + j.ID
		if t := nsToTime(j.SubmitTime); t.After(since) {
			out = append(out, model.Event{Time: t, Source: model.SourceNomad, Kind: model.KindDeployment,
				Severity: model.SeverityInfo, Subject: subject, Title: "Job submitted",
				Detail: fmt.Sprintf("Job %s was submitted or updated.", j.ID)})
		}
		for group, s := range j.JobSummary.Summary {
			if s.Queued > 0 {
				out = append(out, model.Event{Time: c.now(), Source: model.SourceNomad, Kind: model.KindDegraded,
					Severity: model.SeverityCritical, Subject: subject + "/" + group, Title: "Allocations cannot be placed",
					Detail: fmt.Sprintf("%d allocation(s) of %s/%s are queued; check cluster capacity and constraints.", s.Queued, j.ID, group)})
			}
			if s.Lost > 0 {
				out = append(out, model.Event{Time: c.now(), Source: model.SourceNomad, Kind: model.KindDegraded,
					Severity: model.SeverityWarning, Subject: subject + "/" + group, Title: "Lost allocations",
					Detail: fmt.Sprintf("%d allocation(s) of %s/%s are marked lost.", s.Lost, j.ID, group)})
			}
		}
	}
	return out
}

// Nomad deployments carry no timestamp, so a failed deployment is placed at
// its job's submit time and skipped if that is before `since`.
func (c *Collector) deploymentEvents(deps []deployment, submit map[string]time.Time, since time.Time) []model.Event {
	var out []model.Event
	for _, d := range deps {
		if d.Status != "failed" {
			continue
		}
		key := d.Namespace + "/" + d.JobID
		t, ok := submit[key]
		if !ok {
			t = c.now()
		}
		if !t.After(since) {
			continue
		}
		out = append(out, model.Event{Time: t, Source: model.SourceNomad, Kind: model.KindDeployment,
			Severity: model.SeverityCritical, Subject: key, Title: "Deployment failed",
			Detail: fmt.Sprintf("Version %d: %s", d.JobVersion, d.StatusDescription)})
	}
	return out
}

func nsToTime(ns int64) time.Time {
	if ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
