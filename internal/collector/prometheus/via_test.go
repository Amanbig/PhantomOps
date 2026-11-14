package prometheus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// spyTransport records that a request went through it, then forwards it.
type spyTransport struct {
	used  atomic.Int32
	token atomic.Value
	next  http.RoundTripper
}

func (s *spyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.used.Add(1)
	s.token.Store(r.Header.Get("X-Nomad-Token"))
	return s.next.RoundTrip(r)
}

func serve(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

func TestNewViaSendsEveryRequestThroughTheGivenTransport(t *testing.T) {
	base, hits := serve(t)
	rt := &spyTransport{next: http.DefaultTransport}
	c := NewVia(base, nil, rt)
	_, _ = c.Collect(context.Background(), time.Now().Add(-time.Hour))
	if rt.used.Load() == 0 || hits.Load() == 0 {
		t.Fatalf("transport used %d times, server hit %d times; the collector bypassed the tunnel", rt.used.Load(), hits.Load())
	}
	if rt.used.Load() != hits.Load() {
		t.Errorf("%d requests went through the transport but the server saw %d", rt.used.Load(), hits.Load())
	}
}

func TestNewWithoutATransportStillConnectsDirectly(t *testing.T) {
	base, hits := serve(t)
	c := New(base, nil)
	_, _ = c.Collect(context.Background(), time.Now().Add(-time.Hour))
	if hits.Load() == 0 {
		t.Error("the plain constructor must keep working with no tunnel")
	}
}
