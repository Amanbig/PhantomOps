// Package loki collects error-looking log lines from Loki, grouped per
// service, with the first occurrence used as the event time so the line shows
// up where the trouble actually began on the incident timeline.
package loki

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"PhantomOps/internal/httpx"
	"PhantomOps/internal/model"
)

// DefaultQuery selects error-ish lines from every service. service_name is
// populated automatically by Loki 3.x from container/app/job labels.
const DefaultQuery = `{service_name=~".+"} |~ "(?i)(error|fatal|panic|exception|refused|too many connections|timeout|out of memory)"`

const (
	queryLimit    = 1000
	maxSamples    = 3
	critThreshold = 10
)

// groupLabels are tried in order to name the log source.
var groupLabels = []string{"service_name", "container", "app", "pod", "job"}

// Collector implements collector.Collector for Loki.
type Collector struct {
	base  string
	query string
	http  *http.Client
	now   func() time.Time
}

// New creates a Loki collector. Pass an empty query to use DefaultQuery.
func New(baseURL, logQL string) *Collector {
	if logQL == "" {
		logQL = DefaultQuery
	}
	return &Collector{base: baseURL, query: logQL, http: httpx.NewClient(),
		now: func() time.Time { return time.Now().UTC() }}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "loki" }

type queryRangeResp struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"` // [unix ns, line]
		} `json:"result"`
	} `json:"data"`
}

type group struct {
	first   time.Time
	count   int
	samples []sample
}

type sample struct {
	t    time.Time
	line string
}

// Collect implements collector.Collector.
func (c *Collector) Collect(ctx context.Context, since time.Time) ([]model.Event, error) {
	end := c.now()
	q := url.Values{
		"query":     {c.query},
		"start":     {strconv.FormatInt(since.UnixNano(), 10)},
		"end":       {strconv.FormatInt(end.UnixNano(), 10)},
		"limit":     {strconv.Itoa(queryLimit)},
		"direction": {"backward"},
	}
	var resp queryRangeResp
	if err := httpx.GetJSON(ctx, c.http, c.base, "/loki/api/v1/query_range", q, &resp); err != nil {
		return nil, fmt.Errorf("loki query: %w", err)
	}

	groups := map[string]*group{}
	total := 0
	for _, s := range resp.Data.Result {
		name := streamName(s.Stream)
		g := groups[name]
		if g == nil {
			g = &group{}
			groups[name] = g
		}
		for _, v := range s.Values {
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			t := time.Unix(0, ns).UTC()
			g.count++
			total++
			if g.first.IsZero() || t.Before(g.first) {
				g.first = t
			}
			g.samples = append(g.samples, sample{t, v[1]})
		}
	}

	truncated := total >= queryLimit
	var events []model.Event
	for name, g := range groups {
		if g.count == 0 {
			continue
		}
		sort.Slice(g.samples, func(i, j int) bool { return g.samples[i].t.Before(g.samples[j].t) })
		sev := model.SeverityWarning
		if g.count >= critThreshold {
			sev = model.SeverityCritical
		}
		count := strconv.Itoa(g.count)
		if truncated {
			count += "+"
		}
		events = append(events, model.Event{
			Time: g.first, Source: model.SourceLoki, Kind: model.KindLog, Severity: sev, Subject: name,
			Title:  fmt.Sprintf("%s error log line(s)", count),
			Detail: sampleText(g.samples),
		})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	return events, nil
}

func streamName(labels map[string]string) string {
	for _, k := range groupLabels {
		if v := labels[k]; v != "" {
			return v
		}
	}
	return "unknown"
}

func sampleText(s []sample) string {
	if len(s) > maxSamples {
		s = s[len(s)-maxSamples:]
	}
	lines := make([]string, len(s))
	for i, x := range s {
		l := strings.TrimSpace(x.line)
		if len(l) > 200 {
			l = l[:200] + "…"
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}
