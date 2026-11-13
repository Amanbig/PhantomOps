// Package notify sends incident messages to chat webhooks (Slack, Discord)
// or a generic JSON endpoint. Nothing is sent unless the user adds a channel.
//
// Messages are built only from a fixed set of fields (see Message). Raw
// log lines, environment values and credentials never enter a message.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Channel types.
const (
	TypeSlack   = "slack"
	TypeDiscord = "discord"
	TypeGeneric = "generic"
)

// Timeout bounds one delivery so a slow endpoint can never stall monitoring.
const Timeout = 10 * time.Second

// maxText bounds the text sent for the root cause.
const maxText = 400

// Channel is one configured destination. The URL is a secret: it grants the
// right to post, so the UI only ever sees a mask.
type Channel struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	// MinSeverity is "warning" or "critical" (default).
	MinSeverity string `json:"minSeverity"`
}

// Message is the only data that leaves the machine.
type Message struct {
	Kind       string    `json:"event"` // new, reopened, escalated, resolved, test
	IncidentID string    `json:"incidentId,omitempty"`
	Title      string    `json:"title"`
	Severity   string    `json:"severity,omitempty"`
	RootCause  string    `json:"rootCause,omitempty"`
	At         time.Time `json:"time"`
}

// Validate checks a channel's settings.
func (c Channel) Validate() error {
	switch c.Type {
	case TypeSlack, TypeDiscord, TypeGeneric:
	default:
		return fmt.Errorf("unknown channel type %q", c.Type)
	}
	switch c.MinSeverity {
	case "", "warning", "critical":
	default:
		return fmt.Errorf("unknown severity %q", c.MinSeverity)
	}
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || u.Host == "" {
		return errors.New("a valid webhook URL is required")
	}
	// Chat webhooks carry a secret in the URL, so they must be encrypted.
	// A generic endpoint may be plain http (e.g. inside a private network).
	switch u.Scheme {
	case "https":
	case "http":
		if c.Type != TypeGeneric {
			return errors.New("Slack and Discord webhooks must use https")
		}
	default:
		return errors.New("webhook URL must start with https:// (or http:// for a generic endpoint)")
	}
	return nil
}

// Sender delivers messages. The zero value is not usable; use NewSender.
type Sender struct{ client *http.Client }

// NewSender returns a Sender with a short timeout that never follows
// redirects (a redirect could forward the secret URL's payload elsewhere).
func NewSender() *Sender {
	return &Sender{client: &http.Client{
		Timeout:       Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Send delivers one message. Errors never contain the webhook URL.
func (s *Sender) Send(ctx context.Context, ch Channel, m Message) error {
	if err := ch.Validate(); err != nil {
		return err
	}
	body, err := Payload(ch.Type, m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(ch.URL), bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid webhook URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "PhantomOps")
	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL: it is a secret
		}
		return fmt.Errorf("send failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("endpoint answered %s", resp.Status)
	}
	return nil
}

// Payload builds the JSON body for a channel type.
func Payload(typ string, m Message) ([]byte, error) {
	switch typ {
	case TypeSlack:
		return json.Marshal(map[string]string{"text": text(m, "*", 3000)})
	case TypeDiscord:
		return json.Marshal(map[string]any{
			"content":          text(m, "**", 1900),
			"allowed_mentions": map[string]any{"parse": []string{}}, // never ping @everyone
		})
	case TypeGeneric:
		m.Title = clip(oneLine(m.Title), 200)
		m.RootCause = clip(m.RootCause, maxText)
		return json.Marshal(m)
	}
	return nil, fmt.Errorf("unknown channel type %q", typ)
}

// text renders a chat message. bold wraps the title ("*" for Slack, "**" for Discord).
func text(m Message, bold string, limit int) string {
	var b strings.Builder
	b.WriteString(bold + clip(oneLine(m.Title), 200) + bold)
	if rc := clip(oneLine(m.RootCause), maxText); rc != "" {
		b.WriteString("\n" + rc)
	}
	return clip(b.String(), limit)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
