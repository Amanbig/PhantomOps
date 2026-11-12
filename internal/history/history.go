// Package history persists incidents across scans and restarts.
//
// The correlation engine creates a fresh incident on every scan, and its IDs
// are not stable (the detection time of a live-state problem moves with each
// scan). The store matches each scan's incidents to existing records by a
// stable key (environment + incident title, e.g. "Unhealthy service: billing")
// and tracks when each problem was first and last seen, and when it resolved.
package history

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"PhantomOps/internal/model"
)

// Status of a record.
const (
	StatusOpen     = "open"
	StatusResolved = "resolved"
)

const (
	// missesToResolve is how many consecutive scans an incident must be absent
	// before it counts as resolved, so a flapping problem does not reopen and
	// re-notify on every scan.
	missesToResolve = 2
	// reopenWindow: a resolved incident that comes back within this window is
	// reopened instead of creating a new record.
	reopenWindow = 30 * time.Minute
	maxRecords   = 500
	retention    = 30 * 24 * time.Hour
)

// Record is one tracked incident.
type Record struct {
	ID         string         `json:"id"`
	Key        string         `json:"key"`
	Title      string         `json:"title"`
	Severity   model.Severity `json:"severity"`
	Status     string         `json:"status"`
	FirstSeen  time.Time      `json:"firstSeen"`
	LastSeen   time.Time      `json:"lastSeen"`
	ResolvedAt *time.Time     `json:"resolvedAt,omitempty"`
	Scans      int            `json:"scans"`    // scans in which it was present
	Reopened   int            `json:"reopened"` // times it came back after resolving
	// Latest is the most recent snapshot (timeline + investigation).
	Latest model.Incident `json:"latest"`

	misses int // consecutive scans absent (not persisted)
}

// Change describes what a scan did to a record; used for notifications.
type Change struct {
	Kind   string         `json:"kind"` // "new", "escalated", "reopened", "resolved"
	Record Record         `json:"record"`
	From   model.Severity `json:"from,omitempty"` // previous severity for "escalated"
}

// Store is a file-backed incident history. Safe for concurrent use.
type Store struct {
	path    string
	mu      sync.Mutex
	records []*Record
	now     func() time.Time
}

// Open loads the store at path (a missing file is an empty store).
func Open(path string) (*Store, error) {
	s := &Store{path: path, now: func() time.Time { return time.Now().UTC() }}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var recs []*Record
	if err := json.Unmarshal(b, &recs); err != nil {
		// Do not lose the user's file: keep it aside and start fresh.
		_ = os.Rename(path, path+".corrupt")
		return s, errors.New("incident history was unreadable and has been moved to " + filepath.Base(path) + ".corrupt")
	}
	s.records = recs
	return s, nil
}

// Key is the stable identity of an incident.
func Key(inc model.Incident) string { return inc.Environment + "|" + inc.Title }

func newID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func rank(s model.Severity) int {
	switch s {
	case model.SeverityCritical:
		return 2
	case model.SeverityWarning:
		return 1
	}
	return 0
}

// Observe records one complete scan. It rewrites each incident's ID to its
// stable record ID (so callers can use IDs across scans) and returns what
// changed. complete must be false when the scan was partial (e.g. a
// collector failed), in which case absent incidents are not resolved.
func (s *Store) Observe(incidents []model.Incident, complete bool) ([]model.Incident, []Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()

	open := map[string]*Record{}
	recent := map[string]*Record{} // resolved recently, eligible to reopen
	for _, r := range s.records {
		switch {
		case r.Status == StatusOpen:
			open[r.Key] = r
		case r.ResolvedAt != nil && now.Sub(*r.ResolvedAt) <= reopenWindow:
			if cur, ok := recent[r.Key]; !ok || r.LastSeen.After(cur.LastSeen) {
				recent[r.Key] = r
			}
		}
	}

	var changes []Change
	seen := map[*Record]bool{}
	out := make([]model.Incident, len(incidents))
	for i, inc := range incidents {
		key := Key(inc)
		kind := ""
		var from model.Severity

		r := open[key]
		if r != nil && seen[r] {
			r = nil // two incidents with the same key in one scan: keep them apart
		}
		if r == nil {
			if old := recent[key]; old != nil && !seen[old] {
				r, kind = old, "reopened"
				r.Status, r.ResolvedAt = StatusOpen, nil
				r.Reopened++
			}
		}
		if r == nil {
			r, kind = &Record{ID: newID(), Key: key, Status: StatusOpen, FirstSeen: now}, "new"
			if !inc.DetectedAt.IsZero() && inc.DetectedAt.Before(now) {
				r.FirstSeen = inc.DetectedAt
			}
			s.records = append(s.records, r)
		} else if kind == "" && rank(inc.Severity) > rank(r.Severity) {
			kind, from = "escalated", r.Severity
		}

		seen[r] = true
		inc.ID = r.ID
		r.Title, r.Severity, r.LastSeen, r.Latest, r.misses = inc.Title, inc.Severity, now, inc, 0
		r.Scans++
		out[i] = inc
		if kind != "" {
			changes = append(changes, Change{Kind: kind, Record: *r, From: from})
		}
	}

	if complete {
		for _, r := range s.records {
			if r.Status != StatusOpen || seen[r] {
				continue
			}
			r.misses++
			if r.misses >= missesToResolve {
				t := now
				r.Status, r.ResolvedAt, r.misses = StatusResolved, &t, 0
				changes = append(changes, Change{Kind: "resolved", Record: *r})
			}
		}
	}
	s.prune(now)
	return out, changes, s.save()
}

// prune drops old resolved records and caps the total. Callers hold s.mu.
func (s *Store) prune(now time.Time) {
	kept := s.records[:0]
	for _, r := range s.records {
		if r.Status == StatusResolved && r.ResolvedAt != nil && now.Sub(*r.ResolvedAt) > retention {
			continue
		}
		kept = append(kept, r)
	}
	s.records = kept
	if len(s.records) > maxRecords {
		sort.SliceStable(s.records, func(i, j int) bool { return s.records[i].LastSeen.After(s.records[j].LastSeen) })
		s.records = s.records[:maxRecords]
	}
}

// save writes the store atomically with owner-only permissions. Callers hold s.mu.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(s.records)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".history-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// List returns records, open first, then by most recent activity.
func (s *Store) List() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.records))
	for i, r := range s.records {
		out[i] = *r
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Status == StatusOpen) != (out[j].Status == StatusOpen) {
			return out[i].Status == StatusOpen
		}
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out
}

// UpdateInvestigation stores a later investigation (e.g. the AI result) on
// the record with the given ID.
func (s *Store) UpdateInvestigation(id string, inv model.Investigation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.records {
		if r.ID == id {
			r.Latest.Investigation = inv
			return s.save()
		}
	}
	return nil
}

// ClearResolved deletes all resolved records.
func (s *Store) ClearResolved() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.records[:0]
	for _, r := range s.records {
		if r.Status == StatusOpen {
			kept = append(kept, r)
		}
	}
	s.records = kept
	return s.save()
}
