package docker

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"PhantomOps/internal/model"
)

// Thresholds for resource events.
const (
	memWarnRatio     = 0.80
	memCriticalRatio = 0.95
	logTail          = 300
	maxLogSamples    = 3
)

var errorLine = regexp.MustCompile(`(?i)\b(error|fatal|panic|exception|traceback|refused|timeout|too many connections|oom)\b`)

// Collector implements collector.Collector for Docker.
type Collector struct {
	api *apiClient
	now func() time.Time
}

// New creates a Docker collector talking to the given unix socket
// (use DefaultSocket for the standard location).
func New(socket string) *Collector {
	return &Collector{api: newAPIClient(socket), now: func() time.Time { return time.Now().UTC() }}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "docker" }

// Collect implements collector.Collector. It inspects every container and
// emits events for starts, restarts, OOM kills, crashes, unhealthy checks,
// high memory and error-looking log lines.
func (c *Collector) Collect(ctx context.Context, since time.Time) ([]model.Event, error) {
	var list []containerSummary
	if err := c.api.getJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &list); err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	var events []model.Event
	for _, s := range list {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		evs, err := c.collectContainer(ctx, s.ID, since)
		if err != nil {
			// One broken container must not hide the others.
			events = append(events, model.Event{
				Time: c.now(), Source: model.SourceDocker, Kind: model.KindOther,
				Severity: model.SeverityWarning, Subject: firstName(s),
				Title: "Collection failed", Detail: err.Error(),
			})
			continue
		}
		events = append(events, evs...)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	return events, nil
}

func (c *Collector) collectContainer(ctx context.Context, id string, since time.Time) ([]model.Event, error) {
	var in containerInspect
	if err := c.api.getJSON(ctx, "/containers/"+id+"/json", nil, &in); err != nil {
		return nil, err
	}
	name := strings.TrimPrefix(in.Name, "/")
	ev := func(t time.Time, kind model.Kind, sev model.Severity, title, detail string) model.Event {
		return model.Event{Time: t, Source: model.SourceDocker, Kind: kind,
			Severity: sev, Subject: name, Title: title, Detail: detail}
	}

	var out []model.Event
	started := parseTime(in.State.StartedAt)
	finished := parseTime(in.State.FinishedAt)

	if !started.IsZero() && started.After(since) {
		out = append(out, ev(started, model.KindDeployment, model.SeverityInfo,
			"Container started", fmt.Sprintf("%s started from image %s.", name, in.Config.Image)))
	}
	if in.RestartCount > 0 && started.After(since) {
		out = append(out, ev(started, model.KindDegraded, model.SeverityWarning,
			"Container restarted", fmt.Sprintf("%s has restarted %d time(s).", name, in.RestartCount)))
	}
	if !in.State.Running && !finished.IsZero() && finished.After(since) {
		switch {
		case in.State.OOMKilled:
			out = append(out, ev(finished, model.KindResource, model.SeverityCritical,
				"Container OOM-killed", fmt.Sprintf("%s was killed for exceeding its memory limit.", name)))
		case in.State.ExitCode == 143: // SIGTERM: normal `docker stop`
			out = append(out, ev(finished, model.KindOther, model.SeverityInfo,
				"Container stopped", name+" was stopped."))
		case in.State.ExitCode != 0:
			out = append(out, ev(finished, model.KindDegraded, model.SeverityCritical,
				"Container crashed", fmt.Sprintf("%s exited with code %d%s.", name, in.State.ExitCode, exitHint(in.State.ExitCode))))
		}
	}
	if in.State.Health != nil && in.State.Health.Status == "unhealthy" {
		out = append(out, ev(c.now(), model.KindDegraded, model.SeverityCritical,
			"Healthcheck failing", name+" is reported unhealthy."))
	}

	if !in.State.Running {
		return out, nil
	}

	var st containerStats
	if err := c.api.getJSON(ctx, "/containers/"+id+"/stats", url.Values{"stream": {"false"}, "one-shot": {"true"}}, &st); err == nil {
		if lim := st.MemoryStats.Limit; lim > 0 && lim < 1<<60 { // huge limit == unlimited
			ws := st.workingSet()
			ratio := float64(ws) / float64(lim)
			if ratio >= memWarnRatio {
				sev := model.SeverityWarning
				if ratio >= memCriticalRatio {
					sev = model.SeverityCritical
				}
				out = append(out, ev(c.now(), model.KindResource, sev, "High memory usage",
					fmt.Sprintf("%s is using %s of %s (%.0f%%).", name, mb(ws), mb(lim), ratio*100)))
			}
		}
	}

	out = append(out, c.logEvents(ctx, id, name, in.Config.Tty, since)...)
	return out, nil
}

func (c *Collector) logEvents(ctx context.Context, id, name string, tty bool, since time.Time) []model.Event {
	q := url.Values{
		"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"0"},
		"tail": {strconv.Itoa(logTail)}, "since": {strconv.FormatInt(since.Unix(), 10)},
	}
	raw, err := c.api.get(ctx, "/containers/"+id+"/logs", q)
	if err != nil {
		return nil
	}
	var matches []string
	for _, l := range demuxLogs(raw, tty) {
		if errorLine.MatchString(l) {
			matches = append(matches, l)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	samples := matches
	if len(samples) > maxLogSamples {
		samples = samples[len(samples)-maxLogSamples:]
	}
	sev := model.SeverityWarning
	if len(matches) >= 10 {
		sev = model.SeverityCritical
	}
	return []model.Event{{
		Time: c.now(), Source: model.SourceDocker, Kind: model.KindLog, Severity: sev, Subject: name,
		Title:  fmt.Sprintf("%d error log line(s)", len(matches)),
		Detail: strings.Join(truncateAll(samples, 200), "\n"),
	}}
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() <= 1 { // Docker uses 0001-01-01 for "never"
		return time.Time{}
	}
	return t
}

func firstName(s containerSummary) string {
	if len(s.Names) > 0 {
		return strings.TrimPrefix(s.Names[0], "/")
	}
	return s.ID
}

func exitHint(code int) string {
	switch code {
	case 137:
		return " (SIGKILL)"
	case 139:
		return " (SIGSEGV)"
	case 143:
		return " (SIGTERM)"
	}
	return ""
}

func mb(b uint64) string { return fmt.Sprintf("%d MB", b/(1<<20)) }

func truncateAll(in []string, n int) []string {
	out := make([]string, len(in))
	for i, s := range in {
		if len(s) > n {
			s = s[:n] + "…"
		}
		out[i] = s
	}
	return out
}
