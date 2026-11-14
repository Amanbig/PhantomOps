package ssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	// maxSessions stays below sshd's default MaxSessions (10) so parallel
	// dashboard queries cannot get refused.
	maxSessions = 6
	// idleClose is how long an unused connection is kept open.
	idleClose = 30 * time.Second
)

// link is one live SSH connection. It is an interface so tests can fake it.
type link interface {
	exec(ctx context.Context, cmd string) (stdout, stderr string, code int, err error)
	dialTCP(ctx context.Context, addr string) (net.Conn, error)
	close()
}

// Host is a pooled, host-key-verified SSH connection to one server, shared by
// every Docker, Kubernetes, Nomad, Prometheus and Loki connection that runs
// through it. Dashboards rebuild their collectors on every refresh; sharing
// the Host means that costs no new SSH handshake.
//
// The connection closes itself when idle and is re-opened (once per call) if
// it dropped. Callers must only run fixed, allowlisted commands; user input
// must never be concatenated into a command string.
type Host struct {
	dial func(context.Context) (link, error)
	idle time.Duration
	sem  chan struct{}

	mu       sync.Mutex
	l        link
	gen      int // identifies the current connection
	inflight int // running commands plus open forwarded connections
	timer    *time.Timer
}

var (
	registryMu sync.Mutex
	registry   = map[string]*Host{}
)

// Shared returns the pooled Host for cfg, creating it on first use. Two
// connections with the same address, user, key and known_hosts share one SSH
// connection.
func Shared(cfg Config) (*Host, error) {
	cfg, err := normalize(cfg)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s|%s|%s|%s|%t", cfg.Addr, cfg.User, cfg.KeyPath, cfg.KnownHosts, cfg.Insecure)
	registryMu.Lock()
	defer registryMu.Unlock()
	if h := registry[key]; h != nil {
		return h, nil
	}
	h := newHost(func(ctx context.Context) (link, error) {
		l, err := connect(ctx, cfg)
		if err != nil {
			return nil, err // never wrap a nil *sshLink in a non-nil interface
		}
		return l, nil
	}, idleClose)
	registry[key] = h
	return h, nil
}

func newHost(dial func(context.Context) (link, error), idle time.Duration) *Host {
	return &Host{dial: dial, idle: idle, sem: make(chan struct{}, maxSessions)}
}

// Run executes one allowlisted command and returns its stdout, stderr and
// exit status. A non-zero exit status is not an error.
func (h *Host) Run(ctx context.Context, command string) (stdout, stderr string, code int, err error) {
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	case <-ctx.Done():
		return "", "", 0, ctx.Err()
	}
	for attempt := 0; attempt < 2; attempt++ {
		l, gen, derr := h.acquire(ctx)
		if derr != nil {
			return "", "", 0, derr
		}
		stdout, stderr, code, err = l.exec(ctx, command)
		h.release()
		if err == nil || ctx.Err() != nil {
			return stdout, stderr, code, err
		}
		h.drop(gen) // the connection may have dropped; reconnect once
	}
	return stdout, stderr, code, err
}

// DialContext opens a TCP stream to addr from the SSH server's side of the
// network (so "127.0.0.1" means that server). It is meant for
// http.Transport.DialContext. The stream keeps the SSH connection open until
// it is closed. Forwarded streams do not use the command session limit.
func (h *Host) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("ssh tunnel: unsupported network %q", network)
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		l, gen, err := h.acquire(ctx)
		if err != nil {
			return nil, err
		}
		c, err := l.dialTCP(ctx, addr)
		if err == nil {
			return &trackedConn{Conn: c, release: h.release}, nil
		}
		h.release()
		lastErr = err
		var refused *remoteRefused
		if errors.Is(err, ErrForwardingDisabled) || errors.As(err, &refused) || ctx.Err() != nil {
			return nil, err // the SSH link is healthy; asking again will not help
		}
		h.drop(gen) // the SSH link itself failed; reconnect once
	}
	return nil, lastErr
}

// trackedConn keeps the SSH connection counted as busy until it is closed.
type trackedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (t *trackedConn) Close() error {
	err := t.Conn.Close()
	t.once.Do(t.release)
	return err
}

// acquire returns the shared connection, dialing if needed. The returned
// generation identifies it, so drop only discards the connection that failed
// and never one that another call has just re-opened.
func (h *Host) acquire(ctx context.Context) (link, int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	if h.l == nil {
		l, err := h.dial(ctx)
		if err != nil {
			return nil, 0, err
		}
		h.l = l
		h.gen++
	}
	h.inflight++
	return h.l, h.gen, nil
}

func (h *Host) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inflight--
	if h.inflight == 0 && h.l != nil && h.timer == nil {
		h.timer = time.AfterFunc(h.idle, h.closeIdle)
	}
}

func (h *Host) closeIdle() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inflight == 0 {
		h.closeLocked()
	}
}

// drop discards the connection if it is still the one that failed.
func (h *Host) drop(gen int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if gen == h.gen {
		h.closeLocked()
	}
}

func (h *Host) closeLocked() {
	if h.l != nil {
		h.l.close()
	}
	h.l = nil
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
}

// Close shuts the connection immediately.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeLocked()
}
