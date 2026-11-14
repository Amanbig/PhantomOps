// Package prometheus turns PromQL instant-query results into events.
// Each Rule is a query whose series at or above a threshold become events.
package prometheus

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"PhantomOps/internal/httpx"
	"PhantomOps/internal/model"
)

// Rule describes one PromQL check.
type Rule struct {
	Title string // may contain {label} and {value} placeholders
	// Detail may contain {label} and {value} placeholders.
	Detail string
	Query  string // instant query returning one value per series
	// A series produces an event when value >= Warn; critical when Crit > 0 and value >= Crit.
	Warn, Crit float64
	Unit       string // "bytes", "bytes/s", "ratio" or "" for formatting {value}
	Kind       model.Kind
	// SubjectLabels are tried in order to name the event subject.
	SubjectLabels []string
}

// DefaultRules are generic infrastructure checks (cAdvisor + standard metrics).
func DefaultRules() []Rule {
	subj := []string{"name", "container", "pod", "instance", "job"}
	return []Rule{
		{
			Title: "High memory usage", Kind: model.KindResource, Unit: "ratio", Warn: 0.80, Crit: 0.95,
			Query:         `container_memory_working_set_bytes{name!=""} / (container_spec_memory_limit_bytes{name!=""} > 0)`,
			Detail:        "{subject} is using {value} of its memory limit.",
			SubjectLabels: subj,
		},
		{
			Title: "Memory climbing", Kind: model.KindResource, Unit: "bytes/s", Warn: 1e6, Crit: 10e6,
			Query:         `deriv(container_memory_working_set_bytes{name!=""}[1m])`,
			Detail:        "{subject} memory is growing at {value}.",
			SubjectLabels: subj,
		},
		{
			Title: "Scrape target down", Kind: model.KindDegraded, Warn: 1, Crit: 1,
			Query:         `1 - up`,
			Detail:        "Prometheus cannot scrape {subject} (job {job}).",
			SubjectLabels: subj,
		},
		{
			Title: "{alertname}", Kind: model.KindDegraded, Warn: 1,
			Query:         `ALERTS{alertstate="firing"}`,
			Detail:        "Alert firing on {subject}: {summary}",
			SubjectLabels: subj,
		},
		{
			Title: "HTTP 5xx errors", Kind: model.KindDegraded, Unit: "", Warn: 0.1, Crit: 1,
			Query:         `sum by (job, instance) (rate(http_requests_total{status=~"5.."}[1m]))`,
			Detail:        "{subject} is returning 5xx at {value} req/s.",
			SubjectLabels: subj,
		},
	}
}

// Collector implements collector.Collector for Prometheus.
type Collector struct {
	base  string
	http  *http.Client
	rules []Rule
	now   func() time.Time
}

// New creates a collector for the Prometheus server at baseURL.
// Pass nil rules to use DefaultRules.
func New(baseURL string, rules []Rule) *Collector { return NewVia(baseURL, rules, nil) }

// NewVia is New with a custom transport, used to reach Prometheus through an
// SSH tunnel. A nil transport means a direct connection.
func NewVia(baseURL string, rules []Rule, rt http.RoundTripper) *Collector {
	if rules == nil {
		rules = DefaultRules()
	}
	hc := httpx.NewClient()
	if rt != nil {
		hc.Transport = rt
	}
	return &Collector{base: baseURL, http: hc, rules: rules,
		now: func() time.Time { return time.Now().UTC() }}
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "prometheus" }

type queryResp struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"` // [unix ts, "value"]
		} `json:"result"`
	} `json:"data"`
}

// Collect implements collector.Collector. Rules are evaluated against the
// current state and the `since` argument is ignored: a breach that is still
// active is always relevant to an ongoing investigation.
func (c *Collector) Collect(ctx context.Context, _ time.Time) ([]model.Event, error) {
	var events []model.Event
	failed := 0
	var lastErr error
	for _, r := range c.rules {
		evs, err := c.evalRule(ctx, r)
		if err != nil {
			failed++
			lastErr = err
			events = append(events, model.Event{Time: c.now(), Source: model.SourcePrometheus, Kind: model.KindOther,
				Severity: model.SeverityInfo, Subject: "collector", Title: "Partial data",
				Detail: fmt.Sprintf("rule %q: %v", r.Title, err)})
			continue
		}
		events = append(events, evs...)
	}
	if failed == len(c.rules) && failed > 0 {
		return nil, fmt.Errorf("prometheus unreachable or all queries failed: %w", lastErr)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	return events, nil
}

func (c *Collector) evalRule(ctx context.Context, r Rule) ([]model.Event, error) {
	var resp queryResp
	if err := httpx.GetJSON(ctx, c.http, c.base, "/api/v1/query", url.Values{"query": {r.Query}}, &resp); err != nil {
		return nil, err
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("query failed: %s", resp.Error)
	}
	var out []model.Event
	for _, s := range resp.Data.Result {
		str, _ := s.Value[1].(string)
		v, err := strconv.ParseFloat(str, 64)
		if err != nil || v != v || v < r.Warn { // NaN or below threshold
			continue
		}
		sev := model.SeverityWarning
		if r.Crit > 0 && v >= r.Crit {
			sev = model.SeverityCritical
		}
		labels := map[string]string{"value": formatValue(v, r.Unit), "subject": subject(s.Metric, r.SubjectLabels)}
		for k, val := range s.Metric {
			if _, taken := labels[k]; !taken {
				labels[k] = val
			}
		}
		out = append(out, model.Event{
			Time: c.now(), Source: model.SourcePrometheus, Kind: r.Kind, Severity: sev,
			Subject: labels["subject"], Title: expand(r.Title, labels), Detail: expand(r.Detail, labels),
		})
	}
	return out, nil
}

var placeholder = regexp.MustCompile(`\{(\w+)\}`)

func expand(tmpl string, vals map[string]string) string {
	return placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		if v, ok := vals[m[1:len(m)-1]]; ok && v != "" {
			return v
		}
		return "n/a"
	})
}

func subject(metric map[string]string, order []string) string {
	for _, k := range order {
		if v := metric[k]; v != "" {
			return v
		}
	}
	return "unknown"
}

func formatValue(v float64, unit string) string {
	switch unit {
	case "ratio":
		return fmt.Sprintf("%.0f%%", v*100)
	case "bytes":
		return fmt.Sprintf("%.0f MB", v/(1<<20))
	case "bytes/s":
		return fmt.Sprintf("%.1f MB/s", v/(1<<20))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}
