package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var sample = Message{Kind: "new", IncidentID: "abc", Title: "👻 CRITICAL: api crash loop",
	Severity: "critical", RootCause: "api restarted 5 times after deploy v2", At: time.Unix(1700000000, 0).UTC()}

func capture(t *testing.T) (*httptest.Server, *[]byte) {
	t.Helper()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("bad request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestPayloadShapes(t *testing.T) {
	var m map[string]any

	b, err := Payload(TypeSlack, sample)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &m)
	if s, _ := m["text"].(string); !strings.Contains(s, "*👻 CRITICAL: api crash loop*") || !strings.Contains(s, "restarted 5 times") {
		t.Errorf("slack text = %q", s)
	}

	b, _ = Payload(TypeDiscord, sample)
	m = nil
	_ = json.Unmarshal(b, &m)
	if s, _ := m["content"].(string); !strings.Contains(s, "**") {
		t.Errorf("discord content = %q", s)
	}
	am, _ := m["allowed_mentions"].(map[string]any)
	if p, ok := am["parse"].([]any); !ok || len(p) != 0 {
		t.Errorf("discord must not allow mentions: %v", m["allowed_mentions"])
	}

	b, _ = Payload(TypeGeneric, sample)
	m = nil
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"event", "incidentId", "title", "severity", "rootCause", "time"} {
		if _, ok := m[k]; !ok {
			t.Errorf("generic payload missing %q: %s", k, b)
		}
	}
	if len(m) != 6 {
		t.Errorf("generic payload has unexpected fields: %s", b)
	}
}

func TestPayloadIsBounded(t *testing.T) {
	long := sample
	long.RootCause = strings.Repeat("x", 10000)
	long.Title = strings.Repeat("t", 10000) + "\nsecond line"
	for _, typ := range []string{TypeSlack, TypeDiscord, TypeGeneric} {
		b, _ := Payload(typ, long)
		if len(b) > 4000 {
			t.Errorf("%s payload too large: %d bytes", typ, len(b))
		}
	}
	b, _ := Payload(TypeSlack, long)
	if strings.Count(string(b), `\n`) > 1 {
		t.Errorf("title should be one line: %s", b)
	}
}

func TestSendDelivers(t *testing.T) {
	srv, got := capture(t)
	ch := Channel{ID: "1", Type: TypeGeneric, URL: srv.URL}
	if err := NewSender().Send(context.Background(), ch, sample); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(*got), `"incidentId":"abc"`) {
		t.Errorf("body = %s", *got)
	}
}

func TestSendErrorsHideTheURL(t *testing.T) {
	const secret = "s3cr3t-token"
	// non-2xx
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope "+secret, http.StatusForbidden)
	}))
	err := NewSender().Send(context.Background(), Channel{Type: TypeGeneric, URL: srv.URL + "/" + secret}, sample)
	srv.Close()
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), secret) {
		t.Errorf("status error = %v", err)
	}
	// connection refused (server closed)
	err = NewSender().Send(context.Background(), Channel{Type: TypeGeneric, URL: srv.URL + "/" + secret}, sample)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("transport error = %v", err)
	}
}

func TestSendDoesNotFollowRedirects(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	err := NewSender().Send(context.Background(), Channel{Type: TypeGeneric, URL: redir.URL}, sample)
	if err == nil || hit {
		t.Errorf("redirect must fail and not be followed (err=%v hit=%v)", err, hit)
	}
}

func TestSendTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := NewSender().Send(ctx, Channel{Type: TypeGeneric, URL: srv.URL}, sample); err == nil {
		t.Fatal("expected a timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("did not honour context: %v", time.Since(start))
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		ch   Channel
		ok   bool
	}{
		{"slack https", Channel{Type: TypeSlack, URL: "https://hooks.slack.com/services/T/B/x"}, true},
		{"discord https", Channel{Type: TypeDiscord, URL: "https://discord.com/api/webhooks/1/x", MinSeverity: "warning"}, true},
		{"generic http", Channel{Type: TypeGeneric, URL: "http://10.0.0.5:8080/hook"}, true},
		{"slack http rejected", Channel{Type: TypeSlack, URL: "http://hooks.slack.com/x"}, false},
		{"no url", Channel{Type: TypeGeneric}, false},
		{"bad scheme", Channel{Type: TypeGeneric, URL: "ftp://x/y"}, false},
		{"bad type", Channel{Type: "teams", URL: "https://x/y"}, false},
		{"bad severity", Channel{Type: TypeGeneric, URL: "https://x/y", MinSeverity: "info"}, false},
	}
	for _, c := range cases {
		if err := c.ch.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}
