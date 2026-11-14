package ssh

import (
	"fmt"
	"net/http"
	"time"
)

// HTTPTransport returns an http.RoundTripper that sends requests through the
// SSH server: the URL's host and port are resolved and connected from that
// server's side, so "http://127.0.0.1:4646" means that server's own Nomad.
// Nothing listens on the local machine.
//
// It is read-only by construction: any method other than GET or HEAD is
// refused before a connection is made, so a collector bug can never change
// the remote service through the tunnel.
func HTTPTransport(h *Host) http.RoundTripper {
	return readOnly{next: &http.Transport{
		DialContext: h.DialContext,
		// The URL is meant for the SSH server, never for a local proxy.
		Proxy: nil,
		// Keep-alive connections hold the SSH link open, so let them go well
		// before the SSH link's own idle timeout.
		IdleConnTimeout:       15 * time.Second,
		MaxConnsPerHost:       4,
		MaxIdleConnsPerHost:   2,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}}
}

type readOnly struct{ next http.RoundTripper }

func (r readOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case http.MethodGet, http.MethodHead:
		return r.next.RoundTrip(req)
	}
	if req.Body != nil {
		req.Body.Close() // a RoundTripper must close the body even on error
	}
	return nil, fmt.Errorf("ssh tunnel is read-only: refusing %s", req.Method)
}

// CloseIdleConnections lets http.Client.CloseIdleConnections reach the real
// transport through this wrapper.
func (r readOnly) CloseIdleConnections() {
	if c, ok := r.next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
