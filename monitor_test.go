package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"PhantomOps/internal/config"
	"PhantomOps/internal/history"
	"PhantomOps/internal/model"
	"PhantomOps/internal/notify"
	"PhantomOps/internal/remediate"
)

func incident(id, title string, sev model.Severity) model.Incident {
	return model.Incident{ID: id, Title: title, Severity: sev, DetectedAt: time.Now(),
		Investigation: model.Investigation{RootCause: "root cause of " + title}}
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	h, err := history.Open(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.history = h
	a.ctx = context.Background()
	return a
}

func TestBuildNotesHonoursSeverityAndTriage(t *testing.T) {
	a := newTestApp(t)
	_, changes, err := a.history.Observe([]model.Incident{
		incident("c", "crash", model.SeverityCritical),
		incident("w", "warn", model.SeverityWarning),
		incident("m", "muted", model.SeverityCritical),
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.Record.Title == "muted" {
			if _, err := a.history.Mute(c.Record.ID, "test"); err != nil {
				t.Fatal(err)
			}
		}
	}
	crit := a.buildNotes(changes, "critical")
	if len(crit) != 1 || !strings.Contains(crit[0].title, "crash") {
		t.Errorf("critical-only notes = %+v", crit)
	}
	warn := a.buildNotes(changes, "warning")
	if len(warn) != 2 {
		t.Errorf("warning+ notes = %d, want 2 (muted one excluded): %+v", len(warn), warn)
	}
}

func TestCapNotesSummarises(t *testing.T) {
	var notes []note
	for i := 0; i < 8; i++ {
		notes = append(notes, note{id: "x", kind: "new"})
	}
	got := capNotes(notes, 3)
	if len(got) != 3 || got[2].id != "summary" || !strings.Contains(got[2].title, "6 more") {
		t.Errorf("capNotes = %+v", got)
	}
}

func TestWebhooksSendOnlyForEnabledChannelsAndRecordResult(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()

	_, changes, _ := a.history.Observe([]model.Incident{incident("c", "crash", model.SeverityCritical)}, true)
	chans := []notify.Channel{
		{ID: "ok", Type: notify.TypeGeneric, Enabled: true, URL: srv.URL, MinSeverity: "critical"},
		{ID: "off", Type: notify.TypeGeneric, Enabled: false, URL: srv.URL, MinSeverity: "critical"},
		{ID: "bad", Type: notify.TypeGeneric, Enabled: true, URL: bad.URL, MinSeverity: "critical"},
	}
	a.cfg.Webhooks = chans
	a.sendWebhooks(changes, chans)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		n := len(a.hookStatus)
		a.mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("deliveries = %d, want 1 (disabled channel must stay silent): %v", len(bodies), bodies)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(bodies[0]), &m)
	if m["event"] != "new" || m["severity"] != "critical" {
		t.Errorf("payload = %v", m)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.hookStatus["ok"].OK || a.hookStatus["bad"].OK || a.hookStatus["bad"].Error == "" {
		t.Errorf("status = %+v", a.hookStatus)
	}
	if _, ok := a.hookStatus["off"]; ok {
		t.Error("disabled channel must have no status")
	}
}

func TestReportFilename(t *testing.T) {
	now := time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"api crash loop":                  "phantomops-incident-api-crash-loop-2026-03-01.md",
		"../../etc/passwd: \"x\"":         "phantomops-incident-etc-passwd-x-2026-03-01.md",
		"!!!":                             "phantomops-incident-report-2026-03-01.md",
		strings.Repeat("a", 80) + " tail": "phantomops-incident-" + strings.Repeat("a", 40) + "-2026-03-01.md",
	}
	for in, want := range cases {
		if got := reportFilename(in, now); got != want {
			t.Errorf("reportFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIncidentReportFromHistory(t *testing.T) {
	a := newTestApp(t)
	a.audit = remediate.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	_, _, _ = a.history.Observe([]model.Incident{incident("c", "crash", model.SeverityCritical)}, true)
	id := a.history.List()[0].ID
	_ = a.audit.Append(remediate.Entry{User: "u", Phase: "result", IncidentID: id, Action: "rollback", Target: "default/api", OK: true, Message: "done"})

	md, err := a.IncidentReport(id)
	if err != nil || !strings.Contains(md, "# Incident report: crash") || !strings.Contains(md, "default/api") {
		t.Errorf("report = %v\n%s", err, md)
	}
	if _, err := a.IncidentReport("missing"); err == nil {
		t.Error("unknown incident must be an error")
	}
}

func TestRollbackIsRefusedForConnectionsThatReadThroughSSH(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Connections = []config.Connection{
		{ID: "ssh1", Type: config.TypeSSH, Name: "edge", Enabled: true, Addr: "edge:22", User: "ops"},
		{ID: "k1", Type: config.TypeKubernetes, Name: "edge-k3s", Enabled: true, SSHConnection: "ssh1"},
		{ID: "k2", Type: config.TypeKubernetes, Name: "local-k3s", Enabled: true},
	}
	inv := func(conn string) model.Investigation {
		return model.Investigation{Action: "rollback", Target: "default/api", TargetSource: "kubernetes", TargetConnection: conn}
	}
	if _, err := a.executorFor(inv("edge-k3s")); err == nil || !strings.Contains(err.Error(), "monitoring only") {
		t.Errorf("a rollback through SSH must be refused, got %v", err)
	}
	if ex, err := a.executorFor(inv("local-k3s")); err != nil || ex == nil {
		t.Errorf("a direct connection must still be allowed: %v", err)
	}
}

func TestNomadRollbackIsRefusedThroughSSH(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Connections = []config.Connection{
		{ID: "ssh1", Type: config.TypeSSH, Name: "edge", Enabled: true, Addr: "edge:22", User: "ops"},
		{ID: "n1", Type: config.TypeNomad, Name: "edge-nomad", Enabled: true, URL: "http://127.0.0.1:4646", SSHConnection: "ssh1"},
		{ID: "n2", Type: config.TypeNomad, Name: "local-nomad", Enabled: true, URL: "http://127.0.0.1:4646"},
	}
	inv := func(conn string) model.Investigation {
		return model.Investigation{Action: "rollback", Target: "default/web", TargetSource: "nomad", TargetConnection: conn}
	}
	if _, err := a.executorFor(inv("edge-nomad")); err == nil || !strings.Contains(err.Error(), "monitoring only") {
		t.Errorf("a Nomad rollback through SSH must be refused, got %v", err)
	}
	if ex, err := a.executorFor(inv("local-nomad")); err != nil || ex == nil {
		t.Errorf("a direct Nomad connection must still be allowed: %v", err)
	}
}
