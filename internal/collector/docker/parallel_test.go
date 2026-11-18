package docker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDocker answers the remote API calls for n containers named c00, c01, …
// Every container crashed with exit code 3. It records how many requests were
// in flight at once.
type fakeDocker struct {
	n        int
	failID   string // inspect of this container fails
	delay    time.Duration
	inFlight atomic.Int32
	peak     atomic.Int32
	calls    atomic.Int32
}

func (f *fakeDocker) get(ctx context.Context, path string, _ url.Values) ([]byte, error) {
	f.calls.Add(1)
	cur := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		p := f.peak.Load()
		if cur <= p || f.peak.CompareAndSwap(p, cur) {
			break
		}
	}
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	switch {
	case path == "/containers/json":
		var items []string
		for i := 0; i < f.n; i++ {
			items = append(items, fmt.Sprintf(`{"Id":"id%02d","Names":["/c%02d"]}`, i, i))
		}
		return []byte("[" + strings.Join(items, ",") + "]"), nil
	case strings.HasSuffix(path, "/json"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		if id == f.failID {
			return nil, errors.New("inspect exploded")
		}
		name := "c" + strings.TrimPrefix(id, "id")
		return []byte(fmt.Sprintf(`{"Id":%q,"Name":"/%s","State":{"Running":false,"ExitCode":3,"FinishedAt":"2099-01-01T00:00:00Z"},"Config":{"Image":"img"}}`, id, name)), nil
	}
	return []byte(""), nil
}

func newFake(f *fakeDocker) *Collector {
	c := New("/unused")
	c.api.remote = f.get
	return c
}

func TestCollectRunsContainersConcurrentlyWithinTheLimit(t *testing.T) {
	f := &fakeDocker{n: 20, delay: 20 * time.Millisecond}
	evs, err := newFake(f).Collect(context.Background(), time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.peak.Load(); got < 2 {
		t.Errorf("containers were collected one at a time (peak in flight %d)", got)
	}
	// 20 containers, so an unbounded loop would put all 20 in flight.
	if got := f.peak.Load(); got > 6 {
		t.Errorf("more than 6 requests at once: %d", got)
	}
	if len(evs) != 20 {
		t.Fatalf("want one crash event per container (20), got %d", len(evs))
	}
}

func TestCollectKeepsEveryContainerAndIsStable(t *testing.T) {
	// Same input, same events and same order, every time (the engine and the
	// recorded scenarios rely on a stable order).
	var first string
	for i := 0; i < 5; i++ {
		f := &fakeDocker{n: 12, delay: time.Millisecond}
		evs, err := newFake(f).Collect(context.Background(), time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range evs {
			names = append(names, e.Subject+":"+e.Title)
		}
		got := strings.Join(names, ",")
		if i == 0 {
			first = got
			// Events keep the container list order (c00, c01, …), however
			// the requests happen to finish.
			var want []string
			for j := 0; j < 12; j++ {
				want = append(want, fmt.Sprintf("c%02d:Container crashed", j))
			}
			if got != strings.Join(want, ",") {
				t.Errorf("events are not in container list order:\n got %s\nwant %s", got, strings.Join(want, ","))
			}
		} else if got != first {
			t.Fatalf("event order changed between runs:\n%s\n%s", first, got)
		}
	}
}

func TestOneBrokenContainerDoesNotHideTheOthers(t *testing.T) {
	f := &fakeDocker{n: 8, failID: "id03", delay: time.Millisecond}
	evs, err := newFake(f).Collect(context.Background(), time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var crashed, failed int
	for _, e := range evs {
		switch e.Title {
		case "Container crashed":
			crashed++
		case "Collection failed":
			failed++
			if e.Subject != "c03" || !strings.Contains(e.Detail, "inspect exploded") {
				t.Errorf("failure event should name c03 and the cause: %+v", e)
			}
		}
	}
	if crashed != 7 || failed != 1 {
		t.Errorf("want 7 crashes and 1 failure, got %d and %d", crashed, failed)
	}
}

func TestCollectStopsWhenCancelled(t *testing.T) {
	f := &fakeDocker{n: 30, delay: 200 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := newFake(f).Collect(ctx, time.Unix(0, 0))
	if err == nil {
		t.Fatal("a cancelled collect must return an error")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("collect kept running after cancellation (%v)", d)
	}
}
