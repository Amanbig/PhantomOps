package remediate

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one line of the audit log.
type Entry struct {
	Time       time.Time `json:"time"`
	User       string    `json:"user"`
	Phase      string    `json:"phase"` // "approved" (intent, written before acting), "result" or "denied"
	IncidentID string    `json:"incidentId"`
	Action     string    `json:"action"`
	Platform   string    `json:"platform"`
	Connection string    `json:"connection"`
	Target     string    `json:"target"`
	Command    string    `json:"command"`
	OK         bool      `json:"ok"`
	Message    string    `json:"message"`
}

// AuditLog is an append-only JSON-lines file readable only by its owner.
type AuditLog struct {
	path string
	mu   sync.Mutex
}

// NewAuditLog creates a log at path (created on first write).
func NewAuditLog(path string) *AuditLog { return &AuditLog{path: path} }

// Path returns the log file location.
func (l *AuditLog) Path() string { return l.path }

// Append writes one entry. Errors must be handled by the caller: an action
// must not proceed if its intent cannot be recorded.
func (l *AuditLog) Append(e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// Recent returns up to n of the most recent entries, newest first.
func (l *AuditLog) Recent(n int) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			all = append(all, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, n)
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out, nil
}
