package remediate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestAuditAppendAndRecent(t *testing.T) {
	l := NewAuditLog(filepath.Join(t.TempDir(), "sub", "audit.log"))
	if got, err := l.Recent(10); err != nil || len(got) != 0 || got == nil {
		t.Fatalf("missing log: %v %v (want empty, non-nil)", got, err)
	}
	for i := 1; i <= 5; i++ {
		e := Entry{User: "u", Phase: "approved", IncidentID: fmt.Sprint("inc", i), Action: "rollback", Target: "default/api", OK: true}
		if err := l.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.Recent(3)
	if err != nil || len(got) != 3 {
		t.Fatalf("recent = %v %v", got, err)
	}
	if got[0].IncidentID != "inc5" || got[2].IncidentID != "inc3" {
		t.Errorf("must be newest first: %v", got)
	}
	if got[0].Time.IsZero() || time.Since(got[0].Time) > time.Minute {
		t.Errorf("Append must stamp the time: %v", got[0].Time)
	}
	if all, _ := l.Recent(100); len(all) != 5 {
		t.Errorf("all = %d", len(all))
	}
}

func TestAuditFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), "cfg")
	l := NewAuditLog(filepath.Join(dir, "audit.log"))
	if err := l.Append(Entry{User: "u"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{l.Path(): 0o600, dir: 0o700} {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != want {
			t.Errorf("%s mode = %v err=%v, want %v", path, st.Mode().Perm(), err, want)
		}
	}
}

func TestAuditSkipsCorruptLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l := NewAuditLog(path)
	_ = l.Append(Entry{User: "a", IncidentID: "one"})
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{this is not json\n\n")
	f.Close()
	_ = l.Append(Entry{User: "b", IncidentID: "two"})
	got, err := l.Recent(10)
	if err != nil || len(got) != 2 || got[0].IncidentID != "two" || got[1].IncidentID != "one" {
		t.Errorf("got %v err=%v", got, err)
	}
}

func TestAuditFailsLoudlyWhenItCannotWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	// A directory where the file should be: the intent cannot be recorded,
	// so Append must return an error (callers then refuse to act).
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := NewAuditLog(path).Append(Entry{User: "u"}); err == nil {
		t.Error("Append must report failure")
	}
}

func TestAuditConcurrentAppendsDoNotCorruptLines(t *testing.T) {
	l := NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = l.Append(Entry{User: "u", IncidentID: fmt.Sprint(i), Message: "x"})
		}(i)
	}
	wg.Wait()
	if got, _ := l.Recent(100); len(got) != 40 {
		t.Errorf("read %d of 40 entries; lines were lost or corrupted", len(got))
	}
}
