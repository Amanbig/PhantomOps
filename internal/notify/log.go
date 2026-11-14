package notify

import (
	"sync"
	"time"
)

// Delivery is one attempt (or one skipped attempt) to send a message to a
// channel. It never contains the webhook URL: that is a secret, and Send's
// errors do not include it either.
type Delivery struct {
	At         time.Time `json:"at"`
	ChannelID  string    `json:"channelId"`
	Channel    string    `json:"channel"` // the channel's name when it was sent
	Type       string    `json:"type"`
	Kind       string    `json:"kind"` // new, reopened, escalated, resolved, summary, test
	IncidentID string    `json:"incidentId,omitempty"`
	Title      string    `json:"title"`
	OK         bool      `json:"ok"`
	// Skipped means the message was never sent, because an earlier message to
	// the same channel failed in the same scan (a failing endpoint is not
	// hammered).
	Skipped    bool   `json:"skipped,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

func newDelivery(ch Channel, m Message) Delivery {
	return Delivery{At: time.Now().UTC(), ChannelID: ch.ID, Channel: clip(oneLine(ch.Name), 80), Type: ch.Type,
		Kind: m.Kind, IncidentID: m.IncidentID, Title: clip(oneLine(m.Title), 200)}
}

// NewDelivery describes a finished attempt. err is nil on success.
func NewDelivery(ch Channel, m Message, err error, took time.Duration) Delivery {
	d := newDelivery(ch, m)
	d.OK, d.DurationMs = err == nil, took.Milliseconds()
	if err != nil {
		d.Error = clip(oneLine(err.Error()), 300)
	}
	return d
}

// SkippedDelivery describes a message that was not attempted.
func SkippedDelivery(ch Channel, m Message, reason string) Delivery {
	d := newDelivery(ch, m)
	d.Skipped, d.Error = true, clip(oneLine(reason), 300)
	return d
}

// Log keeps the most recent deliveries in memory (it starts empty each time
// the app starts). It is safe for concurrent use.
type Log struct {
	mu    sync.Mutex
	max   int
	items []Delivery // oldest first
}

// NewLog creates a log that keeps at most max entries.
func NewLog(max int) *Log {
	if max < 1 {
		max = 1
	}
	return &Log{max: max}
}

// Add records a delivery, dropping the oldest entry when the log is full.
func (l *Log) Add(d Delivery) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, d)
	if over := len(l.items) - l.max; over > 0 {
		l.items = append([]Delivery(nil), l.items[over:]...)
	}
}

// Recent returns up to n entries, newest first. It never returns nil, so the
// UI receives [] and not null.
func (l *Log) Recent(n int) []Delivery {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.items) {
		n = len(l.items)
	}
	out := make([]Delivery, 0, n)
	for i := len(l.items) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, l.items[i])
	}
	return out
}
