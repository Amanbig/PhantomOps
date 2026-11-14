package ssh

import (
	"context"
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

// CommandRunner runs fixed, read-only commands over one verified SSH
// connection that is shared between calls, so a dashboard that issues many
// queries at once makes a single handshake. The connection closes itself
// when idle and is re-opened (once per call) if it dropped.
//
// Callers must pass only fixed, allowlisted commands; user input must never
// be concatenated into a command string.
type CommandRunner struct {
	dial func(context.Context) (execFull, func(), error)
	idle time.Duration
	sem  chan struct{}

	mu       sync.Mutex
	run      execFull
	closeFn  func()
	gen      int // identifies the current connection
	inflight int
	timer    *time.Timer
}

// NewCommandRunner creates a runner using the same authentication and host
// key verification as the SSH collector.
func NewCommandRunner(cfg Config) (*CommandRunner, error) {
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return newRunner(c.dialFull, idleClose), nil
}

func newRunner(dial func(context.Context) (execFull, func(), error), idle time.Duration) *CommandRunner {
	return &CommandRunner{dial: dial, idle: idle, sem: make(chan struct{}, maxSessions)}
}

// Run executes one allowlisted command and returns its stdout, stderr and
// exit status. A non-zero exit status is not an error.
func (r *CommandRunner) Run(ctx context.Context, command string) (stdout, stderr string, code int, err error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return "", "", 0, ctx.Err()
	}
	for attempt := 0; attempt < 2; attempt++ {
		run, gen, derr := r.acquire(ctx)
		if derr != nil {
			return "", "", 0, derr
		}
		stdout, stderr, code, err = run(ctx, command)
		r.release()
		if err == nil || ctx.Err() != nil {
			return stdout, stderr, code, err
		}
		r.drop(gen) // the connection may have dropped; reconnect once
	}
	return stdout, stderr, code, err
}

// acquire returns the shared connection, dialing if needed. The returned
// generation identifies it, so drop only discards the connection that failed
// and never one that another call has just re-opened.
func (r *CommandRunner) acquire(ctx context.Context) (execFull, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if r.run == nil {
		run, closeFn, err := r.dial(ctx)
		if err != nil {
			return nil, 0, err
		}
		r.run, r.closeFn = run, closeFn
		r.gen++
	}
	r.inflight++
	return r.run, r.gen, nil
}

func (r *CommandRunner) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inflight--
	if r.inflight == 0 && r.run != nil && r.timer == nil {
		r.timer = time.AfterFunc(r.idle, r.closeIdle)
	}
}

func (r *CommandRunner) closeIdle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inflight == 0 {
		r.closeLocked()
	}
}

// drop discards the connection if it is still the one that failed.
func (r *CommandRunner) drop(gen int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen == r.gen {
		r.closeLocked()
	}
}

func (r *CommandRunner) closeLocked() {
	if r.closeFn != nil {
		r.closeFn()
	}
	r.run, r.closeFn = nil, nil
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
}

// Close shuts the connection immediately.
func (r *CommandRunner) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeLocked()
}
