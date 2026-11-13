package engine

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"PhantomOps/internal/collector"
	"PhantomOps/internal/model"
)

var t0 = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

// ev builds an event `min` minutes after t0.
func ev(min float64, src model.Source, kind model.Kind, sev model.Severity, title, detail, subject string) model.Event {
	return model.Event{Time: t0.Add(time.Duration(min * float64(time.Minute))), Source: src, Kind: kind,
		Severity: sev, Title: title, Detail: detail, Subject: subject}
}

func newEngine(mut ...func(*Config)) *Engine {
	cfg := Config{Environment: "prod"}
	for _, m := range mut {
		m(&cfg)
	}
	e := New(cfg)
	e.now = func() time.Time { return t0.Add(time.Hour) }
	return e
}

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %.2f, want %.2f", name, got, want)
	}
}

func analyzeOne(t *testing.T, events []model.Event) model.Incident {
	t.Helper()
	incs, _ := newEngine().Analyze(events)
	if len(incs) != 1 {
		t.Fatalf("got %d incidents, want 1: %+v", len(incs), titles(incs))
	}
	return incs[0]
}

func titles(incs []model.Incident) []string {
	var out []string
	for _, i := range incs {
		out = append(out, i.Title)
	}
	return out
}

// --- hypotheses ------------------------------------------------------------

func TestDeploymentRegressionWithLeakAndDownstream(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceKubernetes, model.KindDeployment, model.SeverityInfo, "Deployment rollout", "api image v2", "default/api"),
		ev(2, model.SourceKubernetes, model.KindResource, model.SeverityWarning, "Memory climbing", "70% of limit", "default/api-5994c75874-cb4vm"),
		ev(4, model.SourceKubernetes, model.KindResource, model.SeverityCritical, "OOMKilled", "exit 137", "default/api-5994c75874-cb4vm"),
		ev(5, model.SourceLoki, model.KindDependency, model.SeverityWarning, "Too many connections", "db: too many connections", "api"),
		ev(6, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Upstream 5xx", "502 from api", "proxy"),
	})
	if inc.Title != "Deployment regression suspected: api" {
		t.Errorf("title = %q", inc.Title)
	}
	inv := inc.Investigation
	approx(t, "confidence", inv.Confidence, 0.90) // .45 base +.20 memory +.10 deps +.10 same service +.05 downstream
	if inv.Action != "rollback" || inv.Target != "default/api" || inv.TargetSource != "kubernetes" {
		t.Errorf("rollback offer = %q %q %q", inv.Action, inv.Target, inv.TargetSource)
	}
	if !strings.Contains(inv.RootCause, "resource leak") || len(inv.Reasoning) < 4 {
		t.Errorf("cause/reasoning = %q / %v", inv.RootCause, inv.Reasoning)
	}
	if inc.Severity != model.SeverityCritical || inc.Environment != "PROD" || !inc.DetectedAt.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("severity/env/detected = %v %v %v", inc.Severity, inc.Environment, inc.DetectedAt)
	}
}

func TestDeploymentBeatsPlainMemoryExhaustionButNotWithoutDeploy(t *testing.T) {
	mem := []model.Event{
		ev(2, model.SourceKubernetes, model.KindResource, model.SeverityWarning, "Memory climbing", "", "default/api"),
		ev(4, model.SourceKubernetes, model.KindResource, model.SeverityCritical, "OOMKilled", "", "default/api"),
	}
	inc := analyzeOne(t, mem)
	if inc.Title != "Memory exhaustion: api" || inc.Investigation.Action != "" {
		t.Errorf("without a deploy: %q action=%q", inc.Title, inc.Investigation.Action)
	}
	approx(t, "memory confidence", inc.Investigation.Confidence, 0.70) // .60 one kill +.10 climbing

	withDeploy := append([]model.Event{ev(0, model.SourceKubernetes, model.KindDeployment, model.SeverityInfo, "Deployment rollout", "", "default/api")}, mem...)
	inc = analyzeOne(t, withDeploy)
	if inc.Title != "Deployment regression suspected: api" {
		t.Errorf("with a deploy: %q", inc.Title)
	}
	approx(t, "deploy confidence", inc.Investigation.Confidence, 0.75)
}

func TestOnlyKubernetesAndNomadDeploysOfferRollback(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		{Time: t0, Source: model.SourceDeploy, Kind: model.KindDeployment, Severity: model.SeverityInfo, Title: "Release v9", Subject: "api"},
		ev(2, model.SourceDocker, model.KindResource, model.SeverityCritical, "OOMKilled", "", "api"),
	})
	if inc.Investigation.Action != "" || inc.Investigation.Target != "" {
		t.Errorf("a generic deploy event must not offer a rollback: %+v", inc.Investigation)
	}
}

func TestDeploymentTooFarBeforeSymptomsIsNotBlamed(t *testing.T) {
	e := newEngine(func(c *Config) { c.Gap = time.Hour })
	incs, _ := e.Analyze([]model.Event{
		ev(0, model.SourceKubernetes, model.KindDeployment, model.SeverityInfo, "Deployment rollout", "", "default/api"),
		ev(20, model.SourceKubernetes, model.KindResource, model.SeverityCritical, "OOMKilled", "", "default/api"),
	})
	if len(incs) != 1 || strings.HasPrefix(incs[0].Title, "Deployment regression") {
		t.Errorf("a deploy 20 min earlier must not be blamed: %v", titles(incs))
	}
}

func TestStartFailureBeatsFirstDeployRollback(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceKubernetes, model.KindDeployment, model.SeverityInfo, "Deployment rollout", "", "default/web"),
		ev(1, model.SourceKubernetes, model.KindOther, model.SeverityCritical, "ImagePullBackOff", "pull access denied for web:v1", "default/web-6d4cf56db6-abcde"),
	})
	if inc.Title != "Startup failure: web" || inc.Investigation.Action != "" {
		t.Errorf("title=%q action=%q", inc.Title, inc.Investigation.Action)
	}
	approx(t, "confidence", inc.Investigation.Confidence, 0.75)
	if !strings.Contains(inc.Investigation.RootCause, "pull access denied") {
		t.Errorf("cause = %q", inc.Investigation.RootCause)
	}
}

func TestStartFailureRepeatBoostsConfidence(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceKubernetes, model.KindOther, model.SeverityCritical, "ErrImagePull", "x", "default/web"),
		ev(1, model.SourceKubernetes, model.KindOther, model.SeverityCritical, "ErrImagePull", "x", "default/web"),
	})
	approx(t, "confidence", inc.Investigation.Confidence, 0.80)
}

func TestMemoryExhaustionCountsRepeatedKills(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceDocker, model.KindResource, model.SeverityCritical, "OOM killed", "", "worker"),
		ev(1, model.SourceDocker, model.KindResource, model.SeverityCritical, "OOM killed", "", "worker"),
	})
	if inc.Title != "Memory exhaustion: worker" || !strings.Contains(inc.Investigation.RootCause, "2 time(s)") {
		t.Errorf("title=%q cause=%q", inc.Title, inc.Investigation.RootCause)
	}
	approx(t, "confidence", inc.Investigation.Confidence, 0.70) // .60 + .10 for repeated kills
}

func TestMemoryPressureWithoutKillIsLowConfidence(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceSSH, model.KindResource, model.SeverityWarning, "Low available memory", "5% free", "host1"),
	})
	// Both the memory and the host hypotheses match; the host one is stronger.
	if inc.Title != "Host resource pressure: host1" {
		t.Errorf("title = %q", inc.Title)
	}
}

func TestDependencyExhaustion(t *testing.T) {
	events := []model.Event{
		ev(0, model.SourceLoki, model.KindLog, model.SeverityWarning, "Connection pool exhausted", "timeout acquiring connection", "orders"),
	}
	inc := analyzeOne(t, events)
	if inc.Title != "Dependency exhaustion: orders" {
		t.Fatalf("title = %q", inc.Title)
	}
	approx(t, "confidence", inc.Investigation.Confidence, 0.50)

	for _, m := range []float64{4, 8} { // spaced beyond the collapse window so they count separately
		events = append(events, ev(m, model.SourceLoki, model.KindLog, model.SeverityWarning, "Connection pool exhausted", "again", "orders"))
	}
	inc = analyzeOne(t, events)
	approx(t, "confidence with 3 reports", inc.Investigation.Confidence, 0.55)
}

func TestUnhealthyServiceUsesProbeOutputAndLogs(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Healthcheck failing", "3 failures. Last probe output: curl: (7) connection refused", "api"),
		ev(1, model.SourceLoki, model.KindLog, model.SeverityWarning, "Error logs", "panic: nil map", "api"),
	})
	if inc.Title != "Unhealthy service: api" {
		t.Fatalf("title = %q", inc.Title)
	}
	approx(t, "confidence", inc.Investigation.Confidence, 0.65) // .55 + .10 for its own error logs
	if !strings.Contains(inc.Investigation.RootCause, "Last probe output: curl: (7) connection refused") {
		t.Errorf("probe output missing: %q", inc.Investigation.RootCause)
	}
}

func TestCrashLoop(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceKubernetes, model.KindOther, model.SeverityWarning, "CrashLoopBackOff", "", "default/web-6d4cf56db6-abcde"),
		ev(1, model.SourceKubernetes, model.KindOther, model.SeverityWarning, "CrashLoopBackOff", "", "default/web-6d4cf56db6-abcde"),
		ev(2, model.SourceKubernetes, model.KindOther, model.SeverityWarning, "CrashLoopBackOff", "", "default/web-6d4cf56db6-abcde"),
	})
	if inc.Title != "Crash loop: web" || !strings.Contains(inc.Investigation.RootCause, "3 related events") {
		t.Errorf("title=%q cause=%q", inc.Title, inc.Investigation.RootCause)
	}
	approx(t, "confidence", inc.Investigation.Confidence, 0.50)
}

func TestHostPressure(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceSSH, model.KindResource, model.SeverityWarning, "Disk almost full", "root filesystem 93% used", "host1"),
		ev(1, model.SourceSSH, model.KindResource, model.SeverityWarning, "High load", "load 12 on 4 cores", "host1"),
	})
	if inc.Title != "Host resource pressure: host1" {
		t.Errorf("title = %q", inc.Title)
	}
	approx(t, "confidence", inc.Investigation.Confidence, 0.60)
}

func TestFallbackWhenNothingMatches(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceDocker, model.KindOther, model.SeverityWarning, "Weird thing", "", "svc-x"),
	})
	if inc.Title != "Anomalies on svc-x" {
		t.Errorf("title = %q", inc.Title)
	}
	approx(t, "confidence", inc.Investigation.Confidence, 0.20)
	if inc.Investigation.Action != "" {
		t.Error("an unclassified anomaly must never offer an action")
	}
}

// The 0.95 cap in analyze is a safety net: with today's rules the highest
// reachable confidence is 0.90, so this only guards the invariant, not the cap
// itself. If a rule is added that can pass 0.95, extend this test.
func TestConfidenceStaysBelowCertainty(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceKubernetes, model.KindDeployment, model.SeverityInfo, "Deployment rollout", "", "default/api"),
		ev(1, model.SourceKubernetes, model.KindResource, model.SeverityWarning, "Memory climbing", "", "default/api"),
		ev(2, model.SourceKubernetes, model.KindResource, model.SeverityCritical, "OOMKilled", "", "default/api"),
		ev(3, model.SourceLoki, model.KindDependency, model.SeverityWarning, "Too many connections", "", "api"),
		ev(4, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Upstream 5xx", "", "proxy"),
	})
	approx(t, "highest reachable confidence", inc.Investigation.Confidence, 0.90)
	if c := inc.Investigation.Confidence; c <= 0 || c > 0.95 {
		t.Errorf("confidence = %v", c)
	}
}

// --- filtering, clustering, splitting ---------------------------------------

func TestNoiseIsDropped(t *testing.T) {
	incs, _ := newEngine().Analyze([]model.Event{
		ev(0, model.SourceDocker, model.KindOther, model.SeverityInfo, "Container started", "", "api"),
		ev(1, model.SourceDocker, model.KindLog, model.SeverityInfo, "Request served", "", "api"),
	})
	if len(incs) != 0 {
		t.Errorf("info noise produced incidents: %v", titles(incs))
	}
	// A deployment alone is a candidate cause, not an incident.
	incs, _ = newEngine().Analyze([]model.Event{
		ev(0, model.SourceKubernetes, model.KindDeployment, model.SeverityInfo, "Deployment rollout", "", "default/api"),
	})
	if len(incs) != 0 {
		t.Errorf("a lone deployment produced incidents: %v", titles(incs))
	}
}

func TestIgnoredSubjectsAndPartialDataWarnings(t *testing.T) {
	e := newEngine(func(c *Config) { c.IgnoreSubjects = []string{"", "CAdvisor"} })
	incs, warns := e.Analyze([]model.Event{
		ev(0, model.SourceDocker, model.KindResource, model.SeverityCritical, "OOMKilled", "", "monitoring-cadvisor-1"),
		{Time: t0, Source: model.SourceLoki, Title: "Partial data", Detail: "query truncated", Severity: model.SeverityWarning},
	})
	if len(incs) != 0 {
		t.Errorf("ignored subject produced incidents: %v", titles(incs))
	}
	if len(warns) != 1 || warns[0] != "loki: query truncated" {
		t.Errorf("warnings = %v", warns)
	}
}

func TestSilenceSplitsIncidents(t *testing.T) {
	incs, _ := newEngine().Analyze([]model.Event{
		ev(0, model.SourceDocker, model.KindResource, model.SeverityCritical, "OOMKilled", "", "api"),
		ev(30, model.SourceDocker, model.KindResource, model.SeverityCritical, "OOMKilled", "", "api"),
	})
	if len(incs) != 2 {
		t.Fatalf("got %d incidents, want 2 (30 min of silence between them)", len(incs))
	}
	if !incs[0].DetectedAt.After(incs[1].DetectedAt) {
		t.Error("incidents must be newest first")
	}
	if incs[0].ID == incs[1].ID {
		t.Error("distinct incidents must have distinct IDs")
	}
}

func TestUnrelatedServicesFailingTogetherAreSeparateIncidents(t *testing.T) {
	incs, _ := newEngine().Analyze([]model.Event{
		ev(0, model.SourceDocker, model.KindResource, model.SeverityCritical, "OOMKilled", "", "billing"),
		ev(0.5, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Healthcheck failing", "", "search"),
	})
	if len(incs) != 2 {
		t.Fatalf("got %d incidents (%v), want 2", len(incs), titles(incs))
	}
}

func TestDownstreamSymptomJoinsTheRightRootCause(t *testing.T) {
	incs, _ := newEngine().Analyze([]model.Event{
		ev(0, model.SourceDocker, model.KindResource, model.SeverityCritical, "OOMKilled", "", "api"),
		ev(0.5, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Upstream 5xx", "502", "proxy"),
		ev(1, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Healthcheck failing", "", "cache"),
	})
	if len(incs) != 2 {
		t.Fatalf("got %d incidents (%v), want 2", len(incs), titles(incs))
	}
	for _, inc := range incs {
		hasProxy := false
		for _, e := range inc.Timeline {
			if e.Subject == "proxy" {
				hasProxy = true
			}
		}
		switch {
		case strings.Contains(inc.Title, "api") && !hasProxy:
			t.Errorf("the proxy 502 belongs with the api failure that preceded it: %v", inc.Timeline)
		case strings.Contains(inc.Title, "cache") && hasProxy:
			t.Errorf("the proxy 502 happened before the cache failure and must not join it")
		}
	}
}

func TestRepeatedEventsCollapse(t *testing.T) {
	inc := analyzeOne(t, []model.Event{
		ev(0, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Container restarted", "exit 1", "api"),
		ev(1, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Container restarted", "exit 1", "api"),
		ev(2, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Container restarted", "exit 1", "api"),
		ev(8, model.SourceDocker, model.KindDegraded, model.SeverityWarning, "Container restarted", "exit 1", "api"),
	})
	if len(inc.Timeline) != 2 {
		t.Fatalf("timeline has %d entries, want 2 (a run of 3, then a recurrence after a long pause): %+v", len(inc.Timeline), inc.Timeline)
	}
	if !strings.Contains(inc.Timeline[0].Detail, "seen 3 times") || strings.Contains(inc.Timeline[1].Detail, "seen") {
		t.Errorf("details = %q / %q", inc.Timeline[0].Detail, inc.Timeline[1].Detail)
	}
}

func TestAnalyzeIsDeterministic(t *testing.T) {
	events := []model.Event{
		ev(2, model.SourceKubernetes, model.KindResource, model.SeverityCritical, "OOMKilled", "", "default/api"),
		ev(0, model.SourceKubernetes, model.KindDeployment, model.SeverityInfo, "Deployment rollout", "", "default/api"),
	} // deliberately out of order
	a, b := analyzeOne(t, events), analyzeOne(t, events)
	if a.ID == "" || a.ID != b.ID || len(a.ID) != 6 {
		t.Errorf("IDs = %q / %q", a.ID, b.ID)
	}
	if !a.Timeline[0].Time.Before(a.Timeline[1].Time) {
		t.Error("timeline must be oldest first")
	}
}

// --- helpers ---------------------------------------------------------------

func TestShortNameAndRelated(t *testing.T) {
	names := map[string]string{
		"phantom-dev/leaky-api-5994c75874-cb4vm": "leaky-api",
		"default/api":                            "api",
		"api":                                    "api",
		"worker-7f9c8d6b5d-x2x4z":                "worker",
	}
	for in, want := range names {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
	rel := []struct {
		a, b string
		want bool
	}{
		{"default/api", "api-5994c75874-cb4vm", true},
		{"default/api", "default/db", false}, // "default" is a stop word
		{"kube-system/coredns", "kube-system/proxy", false},
		{"ab", "ab", false}, // tokens shorter than 3 characters are ignored
		{"leaky-api", "api", true},
	}
	for _, c := range rel {
		if got := related(c.a, c.b); got != c.want {
			t.Errorf("related(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// --- Scan ------------------------------------------------------------------

type fakeCollector struct {
	name   string
	events []model.Event
	err    error
	block  bool
}

func (f fakeCollector) Name() string { return f.name }
func (f fakeCollector) Collect(ctx context.Context, _ time.Time) ([]model.Event, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.events, f.err
}

func TestScanSurvivesFailingAndSlowCollectors(t *testing.T) {
	e := newEngine(func(c *Config) {
		c.Timeout = 50 * time.Millisecond
		c.Collectors = []collector.Collector{
			fakeCollector{name: "docker", events: []model.Event{
				ev(0, model.SourceDocker, model.KindResource, model.SeverityCritical, "OOMKilled", "", "api")}},
			fakeCollector{name: "loki", err: errors.New("connection refused")},
			fakeCollector{name: "nomad", block: true},
		}
	})
	res := e.Scan(context.Background())
	if len(res.Incidents) != 1 {
		t.Errorf("the healthy collector's incident was lost: %v", titles(res.Incidents))
	}
	if res.Errors["loki"] != "connection refused" || !strings.Contains(res.Errors["nomad"], "deadline") || len(res.Errors) != 2 {
		t.Errorf("errors = %v", res.Errors)
	}
	if got := strings.Join(res.Collectors, ","); got != "docker,loki,nomad" {
		t.Errorf("collectors = %s", got)
	}
	if !res.ScannedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("ScannedAt = %v", res.ScannedAt)
	}
	if res.Incidents == nil || res.Warnings == nil {
		t.Error("lists must be non-nil so the UI gets [] and not null")
	}
}

func TestDefaults(t *testing.T) {
	e := New(Config{})
	if e.cfg.Window != 30*time.Minute || e.cfg.Gap != 10*time.Minute || e.cfg.Timeout != 25*time.Second || e.cfg.Environment != "LOCAL" {
		t.Errorf("defaults = %+v", e.cfg)
	}
}

func TestQuietScanReturnsEmptyListsNotNull(t *testing.T) {
	e := newEngine(func(c *Config) { c.Collectors = []collector.Collector{fakeCollector{name: "docker"}} })
	res := e.Scan(context.Background())
	if res.Incidents == nil || res.Warnings == nil || len(res.Incidents) != 0 {
		t.Errorf("incidents=%#v warnings=%#v", res.Incidents, res.Warnings)
	}
}
