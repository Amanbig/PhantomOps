package loki

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"PhantomOps/internal/httpx"
)

// Snapshot is the last hour of log activity in Loki.
type Snapshot struct {
	Type         string    `json:"type"` // "loki"
	TakenAt      time.Time `json:"takenAt"`
	Version      string    `json:"version"`
	Services     []Service `json:"services"`
	ErrorSeries  []Series  `json:"errorSeries"` // error lines per service over time
	RecentErrors []LogLine `json:"recentErrors"`
	Summary      Summary   `json:"summary"`
	Partial      []string  `json:"partial"`
}

// Service is one service's log volume over the window.
type Service struct {
	Name      string  `json:"name"`
	Lines     int64   `json:"lines"`
	Errors    int64   `json:"errors"`
	ErrorRate float64 `json:"errorRate"` // percent of lines
}

// Series is one line on the chart: [unix seconds, value] points.
type Series struct {
	Name   string       `json:"name"`
	Points [][2]float64 `json:"points"`
}

// LogLine is one recent error line.
type LogLine struct {
	Time    time.Time `json:"time"`
	Service string    `json:"service"`
	Line    string    `json:"line"`
}

// Summary holds the headline numbers.
type Summary struct {
	Services int    `json:"services"`
	Lines    int64  `json:"lines"`
	Errors   int64  `json:"errors"`
	Noisiest string `json:"noisiest"` // service with the most errors
}

const (
	snapWindow     = time.Hour
	snapStep       = 5 * time.Minute
	maxServices    = 30
	maxChartSeries = 6
	maxRecent      = 20
)

// errorFilter matches the same error-ish lines as the collector's default
// query, minus lines that quote the filter itself (Loki logs every query it
// runs, so PhantomOps' own queries would otherwise count as errors).
const errorFilter = `|~ "(?i)(error|fatal|panic|exception|refused|too many connections|timeout|out of memory)" ` + selfFilter

// selfFilter drops log lines that contain PhantomOps' own LogQL.
const selfFilter = `!= "error|fatal|panic|exception"`

// Snapshot implements collector.Snapshotter (read-only queries).
func (c *Collector) Snapshot(ctx context.Context) (any, error) {
	now := c.now()
	snap := Snapshot{Type: "loki", TakenAt: now, Services: []Service{}, ErrorSeries: []Series{}, RecentErrors: []LogLine{}, Partial: []string{}}
	selector := `{service_name=~".+"}`
	win := "[" + strconv.Itoa(int(snapWindow.Minutes())) + "m]"
	step := "[" + strconv.Itoa(int(snapStep.Minutes())) + "m]"

	var (
		build struct {
			Version string `json:"version"`
		}
		volume, errorsTotal vectorResp
		errorSeries         matrixResp
		recent              queryRangeResp
	)
	instant := func(q string) url.Values {
		return url.Values{"query": {q}, "time": {strconv.FormatInt(now.UnixNano(), 10)}}
	}
	ranged := func(q string, limit int) url.Values {
		v := url.Values{"query": {q}, "start": {strconv.FormatInt(now.Add(-snapWindow).UnixNano(), 10)},
			"end": {strconv.FormatInt(now.UnixNano(), 10)}}
		if limit > 0 {
			v.Set("limit", strconv.Itoa(limit))
			v.Set("direction", "backward")
		} else {
			v.Set("step", strconv.Itoa(int(snapStep.Seconds())))
		}
		return v
	}
	calls := []struct {
		name string
		path string
		q    url.Values
		out  any
	}{
		{"build info", "/loki/api/v1/status/buildinfo", nil, &build},
		{"log volume", "/loki/api/v1/query", instant(`sum by (service_name) (count_over_time(` + selector + win + `))`), &volume},
		{"error totals", "/loki/api/v1/query", instant(`sum by (service_name) (count_over_time(` + selector + " " + errorFilter + win + `))`), &errorsTotal},
		{"error chart", "/loki/api/v1/query_range", ranged(`sum by (service_name) (count_over_time(`+selector+" "+errorFilter+step+`))`, 0), &errorSeries},
		{"recent errors", "/loki/api/v1/query_range", ranged(selector+" "+errorFilter, maxRecent), &recent},
	}
	errs := make([]error, len(calls))
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = httpx.GetJSON(ctx, c.http, c.base, calls[i].path, calls[i].q, calls[i].out)
		}(i)
	}
	wg.Wait()
	if errs[1] != nil {
		return nil, fmt.Errorf("loki query: %w", errs[1])
	}
	for i, err := range errs {
		if err != nil && i != 1 {
			snap.Partial = append(snap.Partial, fmt.Sprintf("%s: %v", calls[i].name, err))
		}
	}
	snap.Version = build.Version

	svc := map[string]*Service{}
	get := func(name string) *Service {
		if svc[name] == nil {
			svc[name] = &Service{Name: name}
		}
		return svc[name]
	}
	for _, r := range volume.Data.Result {
		get(name(r.Metric)).Lines = r.value()
	}
	for _, r := range errorsTotal.Data.Result {
		get(name(r.Metric)).Errors = r.value()
	}
	for _, s := range svc {
		if s.Lines > 0 {
			s.ErrorRate = float64(s.Errors) / float64(s.Lines) * 100
		}
		snap.Services = append(snap.Services, *s)
		snap.Summary.Lines += s.Lines
		snap.Summary.Errors += s.Errors
	}
	sort.Slice(snap.Services, func(i, j int) bool {
		a, b := snap.Services[i], snap.Services[j]
		if a.Errors != b.Errors {
			return a.Errors > b.Errors
		}
		return a.Lines > b.Lines
	})
	snap.Summary.Services = len(snap.Services)
	if len(snap.Services) > 0 && snap.Services[0].Errors > 0 {
		snap.Summary.Noisiest = snap.Services[0].Name
	}
	if len(snap.Services) > maxServices {
		snap.Services = snap.Services[:maxServices]
	}

	for _, r := range errorSeries.Data.Result {
		s := Series{Name: name(r.Metric), Points: make([][2]float64, 0, len(r.Values))}
		for _, v := range r.Values {
			ts, _ := v[0].(float64)
			f, err := strconv.ParseFloat(fmt.Sprint(v[1]), 64)
			if err == nil {
				s.Points = append(s.Points, [2]float64{ts, f})
			}
		}
		snap.ErrorSeries = append(snap.ErrorSeries, s)
	}
	// Keep the noisiest few series so the chart stays readable.
	sort.Slice(snap.ErrorSeries, func(i, j int) bool { return seriesSum(snap.ErrorSeries[i]) > seriesSum(snap.ErrorSeries[j]) })
	if len(snap.ErrorSeries) > maxChartSeries {
		snap.ErrorSeries = snap.ErrorSeries[:maxChartSeries]
	}

	for _, st := range recent.Data.Result {
		for _, v := range st.Values {
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			line := strings.TrimSpace(v[1])
			if len(line) > 300 {
				line = line[:300] + "…"
			}
			snap.RecentErrors = append(snap.RecentErrors, LogLine{Time: time.Unix(0, ns).UTC(), Service: streamName(st.Stream), Line: line})
		}
	}
	sort.Slice(snap.RecentErrors, func(i, j int) bool { return snap.RecentErrors[i].Time.After(snap.RecentErrors[j].Time) })
	if len(snap.RecentErrors) > maxRecent {
		snap.RecentErrors = snap.RecentErrors[:maxRecent]
	}
	return snap, nil
}

type vectorResp struct {
	Data struct {
		Result []vectorItem `json:"result"`
	} `json:"data"`
}

type vectorItem struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}

func (v vectorItem) value() int64 {
	f, _ := strconv.ParseFloat(fmt.Sprint(v.Value[1]), 64)
	return int64(f)
}

type matrixResp struct {
	Data struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Values [][2]any          `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func name(m map[string]string) string {
	if n := m["service_name"]; n != "" {
		return n
	}
	return "unknown"
}

func seriesSum(s Series) float64 {
	var t float64
	for _, p := range s.Points {
		t += p[1]
	}
	return t
}
