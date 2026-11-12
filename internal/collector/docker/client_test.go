package docker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func collectErr(t *testing.T, socket string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := New(socket).Collect(ctx, time.Now().Add(-time.Minute))
	return err
}

func TestSocketErrorsAreActionable(t *testing.T) {
	dir := t.TempDir()

	// A socket this user may not use, like a user outside the docker group.
	locked := filepath.Join(dir, "locked.sock")
	l, err := net.Listen("unix", locked)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Log("running as root: skipping the permission case")
	} else if err := collectErr(t, locked); err == nil || !strings.Contains(err.Error(), "docker group") {
		t.Errorf("permission denied should mention the docker group, got: %v", err)
	}

	if err := collectErr(t, filepath.Join(dir, "none.sock")); err == nil || !strings.Contains(err.Error(), "is Docker") {
		t.Errorf("missing socket should ask whether Docker is running, got: %v", err)
	}

	// A socket file nobody listens on (Go removes the file on Close unless told not to).
	stale := filepath.Join(dir, "stale.sock")
	l2, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	l2.(*net.UnixListener).SetUnlinkOnClose(false)
	l2.Close()
	if err := collectErr(t, stale); err == nil || !strings.Contains(err.Error(), "daemon running") {
		t.Errorf("refused connection should ask about the daemon, got: %v", err)
	}
}
