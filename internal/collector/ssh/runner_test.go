package ssh

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDialer counts connections and lets a test script each exec.
type fakeDialer struct {
	dials  atomic.Int32
	closes atomic.Int32
	exec   func(n int, cmd string) (string, string, int, error) // n = connection number (1-based)
}

func (f *fakeDialer) dial(context.Context) (execFull, func(), error) {
	n := int(f.dials.Add(1))
	run := func(_ context.Context, cmd string) (string, string, int, error) { return f.exec(n, cmd) }
	return run, func() { f.closes.Add(1) }, nil
}

func ok(_ int, cmd string) (string, string, int, error) { return "out:" + cmd, "", 0, nil }

func TestRunnerSharesOneConnectionAcrossParallelCalls(t *testing.T) {
	f := &fakeDialer{exec: ok}
	r := newRunner(f.dial, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out, _, code, err := r.Run(context.Background(), "x"); err != nil || code != 0 || out != "out:x" {
				t.Errorf("out=%q code=%d err=%v", out, code, err)
			}
		}()
	}
	wg.Wait()
	if n := f.dials.Load(); n != 1 {
		t.Errorf("%d SSH handshakes for 30 commands, want 1", n)
	}
}

func TestRunnerCapsParallelSessions(t *testing.T) {
	var cur, peak atomic.Int32
	gate := make(chan struct{})
	f := &fakeDialer{exec: func(int, string) (string, string, int, error) {
		c := cur.Add(1)
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}
		<-gate
		cur.Add(-1)
		return "", "", 0, nil
	}}
	r := newRunner(f.dial, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, _, _ = r.Run(context.Background(), "x") }()
	}
	time.Sleep(150 * time.Millisecond)
	close(gate)
	wg.Wait()
	// A literal 6, not maxSessions: the limit must stay under sshd's default
	// MaxSessions of 10, and a test that compares a constant with itself
	// cannot notice someone raising it.
	if p := peak.Load(); p > 6 || p < 2 {
		t.Errorf("peak parallel sessions = %d, want between 2 and 6", p)
	}
	if maxSessions >= 10 {
		t.Errorf("maxSessions = %d must stay below sshd's default MaxSessions of 10", maxSessions)
	}
}

func TestRunnerReturnsNonZeroExitAndStderrWithoutError(t *testing.T) {
	f := &fakeDialer{exec: func(int, string) (string, string, int, error) { return "", "kubectl: command not found", 127, nil }}
	r := newRunner(f.dial, time.Minute)
	out, errOut, code, err := r.Run(context.Background(), "kubectl")
	if err != nil || code != 127 || errOut != "kubectl: command not found" || out != "" {
		t.Errorf("out=%q err=%q code=%d err=%v", out, errOut, code, err)
	}
}

func TestRunnerReconnectsOnceWhenTheConnectionDropped(t *testing.T) {
	f := &fakeDialer{exec: func(n int, cmd string) (string, string, int, error) {
		if n == 1 {
			return "", "", 0, errors.New("broken pipe")
		}
		return "fresh", "", 0, nil
	}}
	r := newRunner(f.dial, time.Minute)
	out, _, _, err := r.Run(context.Background(), "x")
	if err != nil || out != "fresh" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if f.dials.Load() != 2 || f.closes.Load() != 1 {
		t.Errorf("dials=%d closes=%d, want 2 and 1 (the dead connection must be closed)", f.dials.Load(), f.closes.Load())
	}
}

func TestRunnerGivesUpAfterOneReconnect(t *testing.T) {
	f := &fakeDialer{exec: func(int, string) (string, string, int, error) { return "", "", 0, errors.New("down") }}
	r := newRunner(f.dial, time.Minute)
	if _, _, _, err := r.Run(context.Background(), "x"); err == nil {
		t.Fatal("expected an error")
	}
	if f.dials.Load() != 2 {
		t.Errorf("dials=%d, want exactly 2 attempts", f.dials.Load())
	}
}

func TestRunnerDoesNotRetryACancelledCall(t *testing.T) {
	f := &fakeDialer{exec: func(int, string) (string, string, int, error) { return "", "", 0, context.Canceled }}
	r := newRunner(f.dial, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := r.Run(ctx, "x"); err == nil {
		t.Fatal("expected an error")
	}
	if f.dials.Load() > 1 {
		t.Errorf("a cancelled call reconnected: dials=%d", f.dials.Load())
	}
}

func TestRunnerClosesWhenIdleAndReopensOnDemand(t *testing.T) {
	f := &fakeDialer{exec: ok}
	r := newRunner(f.dial, 40*time.Millisecond)
	_, _, _, _ = r.Run(context.Background(), "x")
	deadline := time.Now().Add(2 * time.Second)
	for f.closes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.closes.Load() != 1 {
		t.Fatalf("idle connection was not closed (closes=%d)", f.closes.Load())
	}
	if _, _, _, err := r.Run(context.Background(), "y"); err != nil || f.dials.Load() != 2 {
		t.Errorf("reopen: err=%v dials=%d", err, f.dials.Load())
	}
}

func TestRunnerDoesNotCloseAConnectionThatIsInUse(t *testing.T) {
	gate := make(chan struct{})
	f := &fakeDialer{exec: func(int, string) (string, string, int, error) { <-gate; return "done", "", 0, nil }}
	r := newRunner(f.dial, 20*time.Millisecond)
	done := make(chan string)
	go func() { out, _, _, _ := r.Run(context.Background(), "slow"); done <- out }()
	time.Sleep(120 * time.Millisecond) // longer than the idle timeout
	close(gate)
	if out := <-done; out != "done" || f.closes.Load() != 0 {
		t.Errorf("out=%q closes=%d: a running command lost its connection", out, f.closes.Load())
	}
}

func TestRunnerDropIgnoresAStaleGeneration(t *testing.T) {
	f := &fakeDialer{exec: ok}
	r := newRunner(f.dial, time.Minute)
	_, gen1, _ := r.acquire(context.Background())
	r.release()
	r.Close() // generation 1 goes away
	_, gen2, _ := r.acquire(context.Background())
	r.release()
	closesBefore := f.closes.Load()
	r.drop(gen1) // a late failure report about the old connection
	if f.closes.Load() != closesBefore {
		t.Error("a stale failure closed the newer, healthy connection")
	}
	r.drop(gen2)
	if f.closes.Load() != closesBefore+1 {
		t.Error("the current connection should be dropped")
	}
}

func TestRunnerStopsWaitingWhenTheContextEnds(t *testing.T) {
	gate := make(chan struct{})
	f := &fakeDialer{exec: func(int, string) (string, string, int, error) { <-gate; return "", "", 0, nil }}
	r := newRunner(f.dial, time.Minute)
	for i := 0; i < maxSessions; i++ {
		go func() { _, _, _, _ = r.Run(context.Background(), "hold") }()
	}
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, _, _, err := r.Run(ctx, "queued"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err=%v, want a deadline error while all sessions are busy", err)
	}
	close(gate)
}

func TestLimitedBufferRefusesRunawayOutput(t *testing.T) {
	b := &limitedBuffer{max: 10}
	if _, err := b.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("6789012")); err == nil {
		t.Error("output past the limit must be refused")
	}
	if b.buf.String() != "12345" {
		t.Errorf("buffer = %q", b.buf.String())
	}
}

func TestIdleTimerNeverClosesAConnectionWithCommandsInFlight(t *testing.T) {
	// The timer can fire just as a new command acquires the connection.
	f := &fakeDialer{exec: ok}
	r := newRunner(f.dial, time.Minute)
	if _, _, err := r.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.closeIdle() // fires while one command is in flight
	if f.closes.Load() != 0 {
		t.Fatal("the idle timer closed a connection that is in use")
	}
	r.release()
	r.closeIdle() // now it is idle
	if f.closes.Load() != 1 {
		t.Errorf("closes=%d, want 1 once nothing is in flight", f.closes.Load())
	}
}
