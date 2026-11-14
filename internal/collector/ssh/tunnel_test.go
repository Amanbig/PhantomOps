package ssh

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tunnelFixture sends HTTP through HTTPTransport and a fake SSH link into a
// real local HTTP server, recording the address the SSH server was asked for.
type tunnelFixture struct {
	srv      *httptest.Server
	hits     atomic.Int32
	methods  sync.Map
	asked    sync.Map
	dialer   *fakeDialer
	host     *Host
	client   *http.Client
	lastAuth atomic.Value
}

func newTunnel(t *testing.T) *tunnelFixture {
	t.Helper()
	f := &tunnelFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.methods.Store(r.Method, true)
		f.lastAuth.Store(r.Header.Get("X-Nomad-Token"))
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(f.srv.Close)
	f.dialer = &fakeDialer{exec: ok, dialFn: func(_ int, addr string) (net.Conn, error) {
		f.asked.Store(addr, true)
		return net.Dial("tcp", f.srv.Listener.Addr().String()) // what the SSH server would reach
	}}
	f.host = newHost(f.dialer.dial, time.Minute)
	rt := HTTPTransport(f.host)
	f.client = &http.Client{Transport: rt, Timeout: 5 * time.Second}
	t.Cleanup(f.client.CloseIdleConnections)
	return f
}

func TestTunnelSendsGETAndHEADThroughTheSSHHost(t *testing.T) {
	f := newTunnel(t)
	resp, err := f.client.Get("http://127.0.0.1:4646/v1/jobs")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
	if _, found := f.asked.Load("127.0.0.1:4646"); !found {
		t.Error("the URL's own host:port must be what the SSH server is asked to open")
	}
	if resp, err = f.client.Head("http://127.0.0.1:4646/v1/jobs"); err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	resp.Body.Close()
	if f.dialer.dials.Load() != 1 {
		t.Errorf("%d SSH handshakes for two requests, want 1", f.dialer.dials.Load())
	}
}

func TestTunnelIsReadOnlyAndNeverConnectsForOtherMethods(t *testing.T) {
	f := newTunnel(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, "PURGE"} {
		req, _ := http.NewRequest(method, "http://127.0.0.1:4646/v1/job/web/revert", strings.NewReader(`{"JobVersion":1}`))
		resp, err := f.client.Do(req)
		if err == nil {
			resp.Body.Close()
			t.Errorf("%s was sent through the tunnel", method)
			continue
		}
		if !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s: err = %v, want a read-only refusal", method, err)
		}
	}
	if f.hits.Load() != 0 || f.dialer.dials.Load() != 0 {
		t.Errorf("a refused request reached the network: hits=%d SSH handshakes=%d", f.hits.Load(), f.dialer.dials.Load())
	}
}

func TestTunnelClosesTheRequestBodyOfARefusedRequest(t *testing.T) {
	f := newTunnel(t)
	body := &closeSpy{Reader: strings.NewReader("x")}
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:4646/x", body)
	_, _ = f.client.Do(req)
	if !body.closed.Load() {
		t.Error("a RoundTripper must close the request body even when it refuses the request")
	}
}

type closeSpy struct {
	io.Reader
	closed atomic.Bool
}

func (c *closeSpy) Close() error { c.closed.Store(true); return nil }

func TestTunnelIgnoresLocalProxySettings(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	f := newTunnel(t)
	resp, err := f.client.Get("http://nomad.internal:4646/v1/status/leader")
	if err != nil {
		t.Fatalf("a local proxy variable must not hijack the tunnel: %v", err)
	}
	resp.Body.Close()
	if _, found := f.asked.Load("nomad.internal:4646"); !found {
		t.Error("the hostname must be resolved by the SSH server, not locally")
	}
}

func TestTunnelSurfacesForwardingBeingDisabled(t *testing.T) {
	f := newTunnel(t)
	f.dialer.dialFn = func(int, string) (net.Conn, error) { return nil, ErrForwardingDisabled }
	_, err := f.client.Get("http://127.0.0.1:4646/")
	if err == nil || !errors.Is(err, ErrForwardingDisabled) && !strings.Contains(err.Error(), "does not allow port forwarding") {
		t.Errorf("err = %v, want the port-forwarding explanation", err)
	}
}

func TestTunnelLetsTheSSHLinkIdleOnceRequestsFinish(t *testing.T) {
	f := newTunnel(t)
	f.host.idle = 40 * time.Millisecond
	resp, err := f.client.Get("http://127.0.0.1:4646/")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	f.client.CloseIdleConnections() // what the transport's IdleConnTimeout does in production
	deadline := time.Now().Add(2 * time.Second)
	for f.dialer.closes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.dialer.closes.Load() != 1 {
		t.Error("the SSH connection stayed open after the last request finished")
	}
}

func TestTunnelWorksForSeveralServicesOnOneHost(t *testing.T) {
	f := newTunnel(t)
	var wg sync.WaitGroup
	for _, u := range []string{"http://127.0.0.1:4646/", "http://127.0.0.1:9090/", "http://127.0.0.1:3100/"} {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			resp, err := f.client.Do(req)
			if err != nil {
				t.Errorf("%s: %v", u, err)
				return
			}
			resp.Body.Close()
		}(u)
	}
	wg.Wait()
	if f.dialer.dials.Load() != 1 {
		t.Errorf("%d SSH handshakes for three services, want 1", f.dialer.dials.Load())
	}
	for _, addr := range []string{"127.0.0.1:4646", "127.0.0.1:9090", "127.0.0.1:3100"} {
		if _, found := f.asked.Load(addr); !found {
			t.Errorf("%s was never forwarded", addr)
		}
	}
}

func TestTunnelTransportNeverUsesAProxy(t *testing.T) {
	// Go reads proxy variables once per process, so an environment-based test
	// is order dependent; check the configuration directly as well.
	h := newHost((&fakeDialer{exec: ok}).dial, time.Minute)
	tr, ok := HTTPTransport(h).(readOnly).next.(*http.Transport)
	if !ok {
		t.Fatal("the tunnel is expected to wrap an *http.Transport")
	}
	if tr.Proxy != nil {
		t.Error("the tunnel must not use a local proxy: the URL is meant for the SSH server")
	}
	if tr.DialContext == nil {
		t.Error("the tunnel must dial through the SSH host")
	}
}
