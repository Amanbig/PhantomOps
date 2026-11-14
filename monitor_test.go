package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestTrustingAHostKeyIsAuditedAndRejectsNonSSHConnections(t *testing.T) {
	a := newTestApp(t)
	a.audit = remediate.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))

	if _, err := a.InspectSSHHostKey(config.Connection{Type: config.TypeDocker, Name: "d"}); err == nil {
		t.Error("only SSH connections have host keys")
	}
	if _, err := a.TrustSSHHostKey(config.Connection{Type: config.TypeDocker, Name: "d"}, "SHA256:x"); err == nil {
		t.Error("only SSH connections have host keys")
	}
	entries, _ := a.audit.Recent(10)
	if len(entries) != 0 {
		t.Errorf("a rejected request must not be audited: %v", entries)
	}

	// An SSH connection to a port where nothing listens: the attempt fails,
	// and both the intent and the failed result are on record.
	conn := config.Connection{Type: config.TypeSSH, Name: "edge", Addr: "127.0.0.1:1", User: "ops", KnownHosts: filepath.Join(t.TempDir(), "kh")}
	if _, err := a.TrustSSHHostKey(conn, "SHA256:abc"); err == nil {
		t.Fatal("trusting an unreachable host must fail")
	}
	entries, _ = a.audit.Recent(10)
	if len(entries) != 2 || entries[1].Phase != "approved" || entries[0].Phase != "result" || entries[0].OK ||
		entries[0].Action != "trust-host-key" || entries[0].Target != "127.0.0.1:1" {
		t.Errorf("audit trail = %+v, want an approved entry then a failed result", entries)
	}
}

func TestTrustingAHostKeyIsRefusedWhenTheAuditLogCannotBeWritten(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	broken := filepath.Join(dir, "audit.log")
	if err := os.Mkdir(broken, 0o700); err != nil { // a folder where the file should be
		t.Fatal(err)
	}
	a.audit = remediate.NewAuditLog(broken)
	khPath := filepath.Join(dir, "kh")
	conn := config.Connection{Type: config.TypeSSH, Name: "edge", Addr: "127.0.0.1:1", User: "ops", KnownHosts: khPath}
	_, err := a.TrustSSHHostKey(conn, "SHA256:abc")
	if err == nil || !strings.Contains(err.Error(), "audit log") {
		t.Errorf("err = %v, want a refusal because the action cannot be recorded", err)
	}
	if _, serr := os.Stat(khPath); !os.IsNotExist(serr) {
		t.Error("known_hosts was touched although the intent could not be recorded")
	}
}

func waitForDeliveries(t *testing.T, a *App, n int) []notify.Delivery {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if got := a.WebhookDeliveries(100); len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("only %d deliveries were recorded, want %d: %+v", len(a.WebhookDeliveries(100)), n, a.WebhookDeliveries(100))
	return nil
}

func TestWebhookDeliveriesAreLoggedIncludingSkippedAndFailed(t *testing.T) {
	a := newTestApp(t)
	good := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer good.Close()
	const secret = "s3cr3t-path-token"
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()

	_, changes, _ := a.history.Observe([]model.Incident{
		incident("a", "first", model.SeverityCritical),
		incident("b", "second", model.SeverityCritical),
		incident("c", "third", model.SeverityCritical),
	}, true)
	chans := []notify.Channel{
		{ID: "good", Name: "ops", Type: notify.TypeGeneric, Enabled: true, URL: good.URL, MinSeverity: "critical"},
		{ID: "bad", Name: "pager", Type: notify.TypeGeneric, Enabled: true, URL: bad.URL + "/" + secret, MinSeverity: "critical"},
	}
	a.cfg.Webhooks = chans
	a.sendWebhooks(changes, chans)
	got := waitForDeliveries(t, a, 6) // 3 messages x 2 channels

	var ok, failed, skipped int
	for _, d := range got {
		switch {
		case d.Channel == "ops" && d.OK && !d.Skipped:
			ok++
		case d.Channel == "pager" && !d.OK && !d.Skipped && strings.Contains(d.Error, "500"):
			failed++
		case d.Channel == "pager" && d.Skipped && !d.OK:
			skipped++
		default:
			t.Errorf("unexpected record: %+v", d)
		}
		if blob := fmt.Sprintf("%+v", d); strings.Contains(blob, secret) || strings.Contains(blob, bad.URL) || strings.Contains(blob, good.URL) {
			t.Errorf("a delivery record leaks a webhook URL: %s", blob)
		}
	}
	if ok != 3 || failed != 1 || skipped != 2 {
		t.Errorf("ok=%d failed=%d skipped=%d, want 3 delivered, 1 failed and 2 skipped after the failure", ok, failed, skipped)
	}
	if got[0].At.Before(got[len(got)-1].At) {
		t.Error("deliveries must be newest first")
	}
}

func TestTheTestMessageIsLoggedToo(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	if err := a.SendTestWebhook(notify.Channel{Name: "scratch", Type: notify.TypeGeneric, URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	got := a.WebhookDeliveries(10)
	if len(got) != 1 || got[0].Kind != "test" || !got[0].OK || got[0].Channel != "scratch" {
		t.Errorf("deliveries = %+v", got)
	}
	if a.WebhookDeliveries(0) == nil || NewAppEmptyLog(t) == nil {
		t.Error("the list must never be nil")
	}
}

func NewAppEmptyLog(t *testing.T) []notify.Delivery { return NewApp().WebhookDeliveries(5) }

func TestWebhookDeliveriesLimitIsClamped(t *testing.T) {
	a := newTestApp(t)
	for i := 0; i < 150; i++ {
		a.hookLog.Add(notify.Delivery{Title: fmt.Sprint("m", i), OK: true})
	}
	cases := map[int]int{0: 100, -5: 100, 1: 1, 20: 20, 150: 150, 200: 150, 201: 100, 100000: 100}
	for limit, want := range cases {
		if got := len(a.WebhookDeliveries(limit)); got != want {
			t.Errorf("WebhookDeliveries(%d) returned %d entries, want %d", limit, got, want)
		}
	}
}

// The Incidents page shows the incidents a scan returned, and its report
// button asks for a report by that ID. They must be the same ID the history
// stores, or the button would report "not found" for a live incident.
func TestALiveIncidentFromAScanCanBeReported(t *testing.T) {
	a := newTestApp(t)
	live, _, err := a.history.Observe([]model.Incident{incident("scan-local-id", "crash", model.SeverityCritical)}, true)
	if err != nil || len(live) != 1 {
		t.Fatalf("live=%v err=%v", live, err)
	}
	md, err := a.IncidentReport(live[0].ID)
	if err != nil || !strings.Contains(md, "# Incident report: crash") {
		t.Fatalf("a live incident's own ID must produce a report: err=%v\n%s", err, md)
	}
	// ... and still does after the next scan updates the same incident.
	again, _, _ := a.history.Observe([]model.Incident{incident("another-local-id", "crash", model.SeverityCritical)}, true)
	if len(again) != 1 || again[0].ID != live[0].ID {
		t.Errorf("the same incident must keep its ID across scans: %q vs %q", again[0].ID, live[0].ID)
	}
	if _, err := a.IncidentReport(live[0].ID); err != nil {
		t.Errorf("after a second scan: %v", err)
	}
}
