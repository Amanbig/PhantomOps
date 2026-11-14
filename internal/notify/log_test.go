package notify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogKeepsTheNewestEntriesAndDropsTheOldest(t *testing.T) {
	l := NewLog(3)
	for i := 1; i <= 5; i++ {
		l.Add(Delivery{Title: fmt.Sprint("m", i)})
	}
	got := l.Recent(0)
	if len(got) != 3 || got[0].Title != "m5" || got[1].Title != "m4" || got[2].Title != "m3" {
		t.Errorf("recent = %+v, want m5, m4, m3 (newest first, the two oldest dropped)", got)
	}
	if two := l.Recent(2); len(two) != 2 || two[0].Title != "m5" {
		t.Errorf("limit not honoured: %+v", two)
	}
	if all := l.Recent(100); len(all) != 3 {
		t.Errorf("a limit above the size must return what exists: %d", len(all))
	}
}

func TestAnEmptyLogReturnsAnEmptyListNotNull(t *testing.T) {
	if got := NewLog(5).Recent(10); got == nil || len(got) != 0 {
		t.Errorf("got %#v, want a non-nil empty slice so the UI receives []", got)
	}
	if got := NewLog(0).Recent(1); got == nil {
		t.Error("a zero size must not crash or return nil")
	}
}

func TestLogIsSafeForConcurrentUse(t *testing.T) {
	l := NewLog(50)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				l.Add(Delivery{Title: "x"})
				_ = l.Recent(10)
			}
		}()
	}
	wg.Wait()
	if n := len(l.Recent(0)); n != 50 {
		t.Errorf("len = %d, want exactly the cap of 50", n)
	}
}

func TestDeliveriesNeverContainTheWebhookURLEvenWhenTheEndpointFails(t *testing.T) {
	const secret = "T000/B000/s3cr3t-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	url := srv.URL + "/services/" + secret
	ch := Channel{ID: "c1", Type: TypeGeneric, Name: "ops", Enabled: true, URL: url}
	m := Message{Kind: "new", IncidentID: "i1", Title: "👻 CRITICAL: api", At: time.Now()}

	// 1. the endpoint answers with an error
	err := NewSender().Send(context.Background(), ch, m)
	d := NewDelivery(ch, m, err, 12*time.Millisecond)
	// 2. the endpoint is gone (connection refused)
	srv.Close()
	err2 := NewSender().Send(context.Background(), ch, m)
	d2 := NewDelivery(ch, m, err2, time.Millisecond)

	for _, got := range []Delivery{d, d2} {
		blob := fmt.Sprintf("%+v", got)
		for _, leak := range []string{secret, "s3cr3t", "/services/", srv.URL} {
			if strings.Contains(blob, leak) {
				t.Errorf("a delivery record leaks %q: %s", leak, blob)
			}
		}
		if got.OK || got.Error == "" || got.Channel != "ops" || got.Kind != "new" || got.IncidentID != "i1" {
			t.Errorf("record = %+v", got)
		}
	}
	if d.DurationMs != 12 {
		t.Errorf("duration = %d ms", d.DurationMs)
	}
}

func TestDeliveryFieldsAreBoundedAndOneLine(t *testing.T) {
	ch := Channel{ID: "c", Type: TypeSlack, Name: strings.Repeat("n", 500)}
	m := Message{Kind: "new", Title: "line one\nline two " + strings.Repeat("t", 1000)}
	d := NewDelivery(ch, m, errors.New("boom\nsecond line "+strings.Repeat("e", 1000)), 0)
	if len([]rune(d.Channel)) > 81 || len([]rune(d.Title)) > 201 || len([]rune(d.Error)) > 301 {
		t.Errorf("lengths: channel=%d title=%d error=%d", len([]rune(d.Channel)), len([]rune(d.Title)), len([]rune(d.Error)))
	}
	if strings.ContainsAny(d.Title+d.Error, "\n\r") {
		t.Errorf("fields must be single-line: %q %q", d.Title, d.Error)
	}
	if ok := NewDelivery(ch, m, nil, time.Second); !ok.OK || ok.Error != "" || ok.DurationMs != 1000 {
		t.Errorf("success record = %+v", ok)
	}
}

func TestSkippedDeliveriesAreNotSuccessesAndSayWhy(t *testing.T) {
	d := SkippedDelivery(Channel{ID: "c", Name: "ops", Type: TypeSlack}, Message{Kind: "new", Title: "t"}, "an earlier message failed")
	if d.OK || !d.Skipped || d.Error != "an earlier message failed" || d.DurationMs != 0 {
		t.Errorf("record = %+v", d)
	}
}
