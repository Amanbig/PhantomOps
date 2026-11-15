package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// publishInstaller publishes a release whose only download is a fake
// installer named like the Windows one but for the OS the tests run on, so the
// real Install flow (download, checksum, launch) can be exercised anywhere.
func publishInstaller(t *testing.T, g *fakeGitHub) string {
	t.Helper()
	name := "phantomops-" + runtime.GOOS + "-amd64-installer.exe"
	data := []byte("MZ-fake-installer-" + t.Name())
	sum := sha256.Sum256(data)
	g.files[name] = data
	g.files[checksumFile] = []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	return name
}

func withInstallerLauncher(t *testing.T, fn func(path string) error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	old := startInstaller
	startInstaller = func(path string) error {
		calls.Add(1)
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(path)) })
		return fn(path)
	}
	t.Cleanup(func() { startInstaller = old })
	return &calls
}

func TestTheAppClosesItselfOnceTheInstallerHasStarted(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := publishInstaller(t, g)
	launches := withInstallerLauncher(t, func(string) error { return nil })

	u, exe := newUpdater(t)
	var order []string
	u.OnProgress = func(p Progress) {
		if p.Status == "ready" {
			order = append(order, "ready(willQuit="+map[bool]string{true: "true", false: "false"}[p.WillQuit]+")")
		}
	}
	u.OnInstallerStarted = func() { order = append(order, "quit") }
	if _, err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	if launches.Load() != 1 {
		t.Errorf("the installer was launched %d times, want 1", launches.Load())
	}
	// The user must be told before the window goes away.
	if strings.Join(order, ",") != "ready(willQuit=true),quit" {
		t.Errorf("order = %v, want the 'ready' message first, then the request to quit, exactly once", order)
	}
	if p := u.CurrentProgress(); p.Status != "ready" || !p.WillQuit || !strings.Contains(p.Message, "PhantomOps is closing") {
		t.Errorf("progress = %+v", p)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD-BINARY" {
		t.Errorf("the running program must not be touched by an installer-style update: %q", b)
	}
}

func TestTheAppStaysOpenWhenTheInstallerDidNotStart(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := publishInstaller(t, g)
	// The user answered No to the permission prompt.
	withInstallerLauncher(t, func(string) error {
		return errors.New("the installer was not started: the administrator permission prompt was declined")
	})

	u, _ := newUpdater(t)
	quit := false
	u.OnInstallerStarted = func() { quit = true }
	_, _ = u.Check(context.Background())
	err := u.Install(context.Background(), name)
	if err == nil || !strings.Contains(err.Error(), "prompt was declined") {
		t.Fatalf("err = %v, want the launcher's explanation", err)
	}
	if quit {
		t.Fatal("the app closed although the installer never started; the user would lose their running app for nothing")
	}
	if p := u.CurrentProgress(); p.Status != "error" || p.WillQuit {
		t.Errorf("progress = %+v, want an error and no quit", p)
	}
}

func TestNothingElseMakesTheAppClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot replace a running file on Windows")
	}
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := g.release(t, "NEW-BINARY") // an in-place .tar.gz update
	u, _ := newUpdater(t)
	quit := false
	u.OnInstallerStarted = func() { quit = true }
	_, _ = u.Check(context.Background())
	if err := u.Install(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	if quit || u.CurrentProgress().WillQuit {
		t.Error("an in-place update keeps the user's 'Restart now' button; only the Windows installer closes the app")
	}
}

func TestAMissingQuitHookIsHarmless(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := publishInstaller(t, g)
	withInstallerLauncher(t, func(string) error { return nil })
	u, _ := newUpdater(t) // OnInstallerStarted is nil
	_, _ = u.Check(context.Background())
	if err := u.Install(context.Background(), name); err != nil {
		t.Errorf("err = %v", err)
	}
}
