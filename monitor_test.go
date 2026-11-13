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

	"PhantomOps/internal/history"
	"PhantomOps/internal/model"
	"PhantomOps/internal/notify"
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
