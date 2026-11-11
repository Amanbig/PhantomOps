// Package engine is PhantomOps' correlation engine. It runs collectors,
// filters noise, groups events into incidents by time proximity, builds a
// timeline for each, and scores a rule-based root-cause hypothesis.
package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"PhantomOps/internal/collector"
	"PhantomOps/internal/model"
)

// Config tunes the engine.
type Config struct {
	Collectors []collector.Collector
	// Window is how far back collectors look (default 30m).
	Window time.Duration
	// Gap is the maximum silence between events of one incident (default 10m).
	Gap time.Duration
	// Timeout bounds each collector (default 25s).
	Timeout     time.Duration
	Environment string
	// IgnoreSubjects drops events whose subject contains any of these
	// case-insensitive substrings (e.g. the monitoring stack itself).
	IgnoreSubjects []string
}

// Engine correlates events into incidents.
type Engine struct {
	cfg Config
	now func() time.Time
}

// Result is the outcome of one scan.
type Result struct {
	Incidents  []model.Incident  `json:"incidents"`
	Collectors []string          `json:"collectors"` // collectors that ran
	Errors     map[string]string `json:"errors"`     // collector -> failure
	Warnings   []string          `json:"warnings"`   // partial-data notes
	ScannedAt  time.Time         `json:"scannedAt"`
}

// New creates an engine, applying defaults.
func New(cfg Config) *Engine {
	if cfg.Window <= 0 {
		cfg.Window = 30 * time.Minute
	}
	if cfg.Gap <= 0 {
		cfg.Gap = 10 * time.Minute
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 25 * time.Second
	}
	if cfg.Environment == "" {
		cfg.Environment = "LOCAL"
	}
	return &Engine{cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
}

// Scan runs every collector concurrently and analyzes the combined events.
func (e *Engine) Scan(ctx context.Context) Result {
	now := e.now()
	since := now.Add(-e.cfg.Window)

	type out struct {
		name   string
		events []model.Event
		err    error
	}
	results := make([]out, len(e.cfg.Collectors))
	var wg sync.WaitGroup
	for i, c := range e.cfg.Collectors {
		wg.Add(1)
		go func(i int, c collector.Collector) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
			defer cancel()
			evs, err := c.Collect(cctx, since)
			results[i] = out{c.Name(), evs, err}
		}(i, c)
	}
	wg.Wait()

	res := Result{Errors: map[string]string{}, ScannedAt: now, Incidents: []model.Incident{}, Warnings: []string{}}
	var all []model.Event
	for _, r := range results {
		res.Collectors = append(res.Collectors, r.name)
		if r.err != nil {
			res.Errors[r.name] = r.err.Error()
			continue
		}
		all = append(all, r.events...)
	}
	incidents, warnings := e.Analyze(all)
	res.Incidents, res.Warnings = incidents, warnings
	return res
}

// Analyze turns raw events into incidents (newest first). It also returns
// notes about partial data reported by collectors.
func (e *Engine) Analyze(events []model.Event) ([]model.Incident, []string) {
	var warnings []string
	var kept []model.Event
	for _, ev := range events {
		if ev.Title == "Partial data" {
			warnings = append(warnings, fmt.Sprintf("%s: %s", ev.Source, ev.Detail))
			continue
		}
		if e.ignored(ev) || !relevant(ev) {
			continue
		}
		kept = append(kept, ev)
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Time.Before(kept[j].Time) })

	var incidents []model.Incident
	for _, window := range e.cluster(kept) {
		for _, group := range splitByService(window) {
			if inc, ok := e.buildIncident(group); ok {
				incidents = append(incidents, inc)
			}
		}
	}
	sort.SliceStable(incidents, func(i, j int) bool { return incidents[i].DetectedAt.After(incidents[j].DetectedAt) })
	return incidents, warnings
}

func (e *Engine) ignored(ev model.Event) bool {
	s := strings.ToLower(ev.Subject)
	for _, sub := range e.cfg.IgnoreSubjects {
		if sub != "" && strings.Contains(s, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// relevant keeps warnings/criticals and deployment-like info events (which
// are root-cause candidates); other info events are noise.
func relevant(ev model.Event) bool {
	return ev.Severity != model.SeverityInfo || ev.Kind == model.KindDeployment
}

// cluster splits time-ordered events wherever the silence exceeds Gap and
// keeps only clusters that contain at least one warning or critical event.
func (e *Engine) cluster(events []model.Event) [][]model.Event {
	var clusters [][]model.Event
	var cur []model.Event
	flush := func() {
		for _, ev := range cur {
			if ev.Severity != model.SeverityInfo {
				clusters = append(clusters, cur)
				break
			}
		}
		cur = nil
	}
	for _, ev := range events {
		if len(cur) > 0 && ev.Time.Sub(cur[len(cur)-1].Time) > e.cfg.Gap {
			flush()
		}
		cur = append(cur, ev)
	}
	flush()
	return clusters
}

// occurrence is a timeline entry that may represent repeated events.
type occurrence struct {
	model.Event
	Count int
}

// collapseGap is the longest pause between two repeats of the same event that
// still counts as one ongoing occurrence. A longer pause (for example a new
// deployment followed by fresh failures) starts a new entry, so recurrences
// stay visible on the timeline and in the analysis.
const collapseGap = 3 * time.Minute

// collapse merges runs of the same (source, title, subject), keeping the
// first occurrence's time.
func collapse(events []model.Event) []occurrence {
	type run struct {
		idx  int
		last time.Time
	}
	runs := map[string]*run{}
	var out []occurrence
	for _, ev := range events {
		k := string(ev.Source) + "|" + ev.Title + "|" + ev.Subject
		if r, ok := runs[k]; ok && ev.Time.Sub(r.last) <= collapseGap {
			out[r.idx].Count++
			r.last = ev.Time
			continue
		}
		runs[k] = &run{idx: len(out), last: ev.Time}
		out = append(out, occurrence{Event: ev, Count: 1})
	}
	return out
}

func (e *Engine) buildIncident(events []model.Event) (model.Incident, bool) {
	occ := collapse(events)

	var detected time.Time
	sev := model.SeverityInfo
	for _, o := range occ {
		if o.Severity == model.SeverityInfo {
			continue
		}
		if detected.IsZero() || o.Time.Before(detected) {
			detected = o.Time
		}
		if rank(o.Severity) > rank(sev) {
			sev = o.Severity
		}
	}
	if detected.IsZero() {
		return model.Incident{}, false
	}

	f := analyze(occ)

	timeline := make([]model.Event, len(occ))
	for i, o := range occ {
		ev := o.Event
		if o.Count > 1 {
			ev.Detail = strings.TrimSpace(ev.Detail + fmt.Sprintf(" (seen %d times)", o.Count))
		}
		timeline[i] = ev
	}

	h := sha1.Sum([]byte(detected.Truncate(time.Minute).Format(time.RFC3339) + "|" + f.headline))
	return model.Incident{
		ID:          hex.EncodeToString(h[:])[:6],
		Title:       f.headline,
		Severity:    sev,
		Environment: strings.ToUpper(e.cfg.Environment),
		DetectedAt:  detected,
		Automatic:   true,
		Timeline:    timeline,
		Investigation: model.Investigation{
			RootCause:         f.cause,
			Confidence:        f.confidence,
			RecommendedAction: f.recommendation,
			Action:            f.action,
			Target:            f.target,
			TargetSource:      f.targetSource,
			TargetConnection:  f.targetOrigin,
			Reasoning:         f.reasons,
		},
	}, true
}

func rank(s model.Severity) int {
	switch s {
	case model.SeverityCritical:
		return 2
	case model.SeverityWarning:
		return 1
	}
	return 0
}
