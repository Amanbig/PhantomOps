package prometheus

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"PhantomOps/internal/httpx"
)

// Snapshot is the current state of a Prometheus server.
type Snapshot struct {
	Type    string    `json:"type"` // "prometheus"
	TakenAt time.Time `json:"takenAt"`
	Version string    `json:"version"`
	Targets []Target  `json:"targets"`
	Alerts  []Alert   `json:"alerts"`
	Charts  []Chart   `json:"charts"`
	Summary Summary   `json:"summary"`
	Partial []string  `json:"partial"`
}

// Target is one active scrape target.
type Target struct {
	Job        string    `json:"job"`
	Instance   string    `json:"instance"`
	Health     string    `json:"health"` // up, down, unknown
	LastError  string    `json:"lastError"`
	LastScrape time.Time `json:"lastScrape"`
	DurationMs float64   `json:"durationMs"`
}

// Alert is a firing or pending alert.
type Alert struct {
	Name     string            `json:"name"`
	State    string            `json:"state"` // firing, pending
	Severity string            `json:"severity"`
	Summary  string            `json:"summary"`
	Labels   map[string]string `json:"labels"`
	Since    time.Time         `json:"since"`
}

// Chart is a set of time series for one panel.
type Chart struct {
	Title  string   `json:"title"`
	Unit   string   `json:"unit"` // "", "percent", "per-second"
	Series []Series `json:"series"`
}

// Series is one line: [unix seconds, value] points.
type Series struct {
	Name   string       `json:"name"`
	Points [][2]float64 `json:"points"`
}

// Summary holds the headline numbers.
type Summary struct {
	TargetsUp int `json:"targetsUp"`
	Targets   int `json:"targets"`
	Firing    int `json:"firing"`
	Pending   int `json:"pending"`
	Jobs      int `json:"jobs"`
}

const (
	chartRange = time.Hour
	chartStep  = time.Minute
)

// charts are generic panels that work on any Prometheus server.
var charts = []struct {
	title, unit, query, legend string
}{
	{"Targets up", "percent", `100 * sum by (job) (up) / count by (job) (up)`, "job"},
	{"Firing alerts", "", `count by (alertname) (ALERTS{alertstate="firing"})`, "alertname"},
	{"Samples ingested", "per-second", `sum(rate(prometheus_tsdb_head_samples_appended_total[2m]))`, ""},
}

// Snapshot implements collector.Snapshotter (read-only API calls).
func (c *Collector) Snapshot(ctx context.Context) (any, error) {
	now := c.now()
	snap := Snapshot{Type: "prometheus", TakenAt: now, Targets: []Target{}, Alerts: []Alert{}, Charts: []Chart{}, Partial: []string{}}

	var (
		build struct {
			Data struct {
				Version string `json:"version"`
			} `json:"data"`
		}
		targets struct {
			Data struct {
				ActiveTargets []struct {
					Labels             map[string]string `json:"labels"`
					ScrapePool         string            `json:"scrapePool"`
					Health             string            `json:"health"`
					LastError          string            `json:"lastError"`
					LastScrape         time.Time         `json:"lastScrape"`
					LastScrapeDuration float64           `json:"lastScrapeDuration"`
				} `json:"activeTargets"`
			} `json:"data"`
		}
		alerts struct {
			Data struct {
				Alerts []struct {
					Labels      map[string]string `json:"labels"`
					Annotations map[string]string `json:"annotations"`
					State       string            `json:"state"`
					ActiveAt    time.Time         `json:"activeAt"`
				} `json:"alerts"`
			} `json:"data"`
		}
	)
	ranges := make([]rangeResp, len(charts))
	errs := make([]error, 3+len(charts))
	var wg sync.WaitGroup
	run := func(i int, path string, q url.Values, out any) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = httpx.GetJSON(ctx, c.http, c.base, path, q, out)
		}()
	}
	run(0, "/api/v1/status/buildinfo", nil, &build)
	run(1, "/api/v1/targets", url.Values{"state": {"active"}}, &targets)
	run(2, "/api/v1/alerts", nil, &alerts)
	start, end := now.Add(-chartRange), now
	for i, ch := range charts {
		run(3+i, "/api/v1/query_range", url.Values{
			"query": {ch.query},
			"start": {strconv.FormatInt(start.Unix(), 10)},
			"end":   {strconv.FormatInt(end.Unix(), 10)},
			"step":  {strconv.Itoa(int(chartStep.Seconds()))},
		}, &ranges[i])
	}
	wg.Wait()
	if errs[1] != nil {
		return nil, fmt.Errorf("prometheus targets: %w", errs[1])
	}
	names := []string{"build info", "targets", "alerts"}
	for i, err := range errs {
		if err == nil || i == 1 {
			continue
		}
		what := "chart"
		if i < 3 {
			what = names[i]
		} else {
			what = "chart " + charts[i-3].title
		}
		snap.Partial = append(snap.Partial, fmt.Sprintf("%s: %v", what, err))
	}
	snap.Version = build.Data.Version

	jobs := map[string]bool{}
	for _, t := range targets.Data.ActiveTargets {
		job := t.Labels["job"]
		if job == "" {
			job = t.ScrapePool
		}
		jobs[job] = true
		snap.Targets = append(snap.Targets, Target{Job: job, Instance: t.Labels["instance"], Health: t.Health,
			LastError: t.LastError, LastScrape: t.LastScrape, DurationMs: t.LastScrapeDuration * 1000})
		snap.Summary.Targets++
		if t.Health == "up" {
			snap.Summary.TargetsUp++
		}
	}
	snap.Summary.Jobs = len(jobs)
	sort.SliceStable(snap.Targets, func(i, j int) bool {
		a, b := snap.Targets[i], snap.Targets[j]
		if (a.Health == "up") != (b.Health == "up") {
			return a.Health != "up"
		}
		return a.Job+a.Instance < b.Job+b.Instance
	})

	for _, a := range alerts.Data.Alerts {
		al := Alert{Name: a.Labels["alertname"], State: a.State, Severity: a.Labels["severity"],
			Summary: a.Annotations["summary"], Labels: a.Labels, Since: a.ActiveAt}
		if al.Summary == "" {
			al.Summary = a.Annotations["description"]
		}
		snap.Alerts = append(snap.Alerts, al)
		switch a.State {
		case "firing":
			snap.Summary.Firing++
		case "pending":
			snap.Summary.Pending++
		}
	}
	sort.SliceStable(snap.Alerts, func(i, j int) bool {
		if snap.Alerts[i].State != snap.Alerts[j].State {
			return snap.Alerts[i].State == "firing"
		}
		return snap.Alerts[i].Since.Before(snap.Alerts[j].Since)
	})

	for i, ch := range charts {
		if errs[3+i] != nil {
			continue
		}
		out := Chart{Title: ch.title, Unit: ch.unit, Series: []Series{}}
		for _, r := range ranges[i].Data.Result {
			name := r.Metric[ch.legend]
			if name == "" {
				name = ch.title
			}
			s := Series{Name: name, Points: make([][2]float64, 0, len(r.Values))}
			for _, v := range r.Values {
				ts, _ := v[0].(float64)
				str, _ := v[1].(string)
				f, err := strconv.ParseFloat(str, 64)
				if err != nil || f != f {
					continue
				}
				s.Points = append(s.Points, [2]float64{ts, f})
			}
			out.Series = append(out.Series, s)
		}
		sort.Slice(out.Series, func(a, b int) bool { return out.Series[a].Name < out.Series[b].Name })
		snap.Charts = append(snap.Charts, out)
	}
	return snap, nil
}

type rangeResp struct {
	Data struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Values [][2]any          `json:"values"`
		} `json:"result"`
	} `json:"data"`
}
