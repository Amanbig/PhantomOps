package updater

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestShellExecuteResult(t *testing.T) {
	cases := []struct {
		name    string
		ret     uintptr
		lastErr error
		want    string // "" means success
	}{
		{"success returns a handle above 32", 42, syscall.Errno(0), ""},
		{"a large handle is success", 0x1234, nil, ""},
		{"32 is not success", 32, syscall.Errno(0), "ShellExecute code 32"},
		{"permission prompt declined (ERROR_CANCELLED)", 5, syscall.Errno(1223), "prompt was declined"},
		{"declined, reported only through the error", 0, syscall.Errno(1223), "prompt was declined"},
		{"access denied", 5, syscall.Errno(0), "denied permission"},
		{"file not found", 2, syscall.Errno(0), "could not be found"},
		{"path not found", 3, syscall.Errno(0), "could not be found"},
		{"anything else is named, not guessed", 27, syscall.Errno(0), "ShellExecute code 27"},
	}
	for _, c := range cases {
		err := shellExecuteResult(c.ret, c.lastErr)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
}

func TestEveryFailureSaysTheInstallerDidNotStart(t *testing.T) {
	for _, ret := range []uintptr{0, 2, 3, 5, 8, 11, 26, 27, 28, 29, 30, 31, 32} {
		if err := shellExecuteResult(ret, syscall.Errno(0)); err == nil || !strings.HasPrefix(err.Error(), "the installer was not started") {
			t.Errorf("ret=%d: err = %v", ret, err)
		}
	}
}

// The verb is what makes Windows show the Yes/No permission prompt. Without
// it (or with "open" on an installer whose manifest is missing) the install
// fails with "the requested operation requires elevation".
func TestTheInstallerIsLaunchedWithTheAdministratorVerb(t *testing.T) {
	if shellExecuteVerbUI != "runas" {
		t.Errorf("verb = %q, want runas", shellExecuteVerbUI)
	}
}

func TestInstallingAnExeHandsTheDownloadedFileToTheLauncher(t *testing.T) {
	var gotPath string
	var gotBytes []byte
	old := startInstaller
	startInstaller = func(path string) error {
		gotPath = path
		gotBytes, _ = os.ReadFile(path)
		return nil
	}
	t.Cleanup(func() { startInstaller = old })

	payload := []byte("MZ-fake-nsis-installer-bytes")
	name := "phantomops-windows-amd64-installer.exe"
	u := New()
	if err := u.apply(name, payload, "unused-target"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(gotPath)) })
	if filepath.Base(gotPath) != name {
		t.Errorf("launched %q, want a file named %q", gotPath, name)
	}
	if string(gotBytes) != string(payload) {
		t.Errorf("the launcher saw %q, want the downloaded bytes", gotBytes)
	}
}

func TestAnInstallerThatCannotStartIsReportedNotIgnored(t *testing.T) {
	old := startInstaller
	startInstaller = func(string) error { return errors.New("the installer was not started: nope") }
	t.Cleanup(func() { startInstaller = old })
	err := New().apply("phantomops-windows-amd64-installer.exe", []byte("x"), "unused")
	if err == nil || !strings.Contains(err.Error(), "not started") {
		t.Errorf("err = %v, want the launcher's error to reach the user", err)
	}
}

func TestOtherInstallTypesDoNotUseTheWindowsLauncher(t *testing.T) {
	old := startInstaller
	called := false
	startInstaller = func(string) error { called = true; return nil }
	t.Cleanup(func() { startInstaller = old })
	// An unreadable archive fails before anything is launched.
	_ = New().apply("phantomops-linux-amd64.tar.gz", []byte("not an archive"), "unused")
	if called {
		t.Error("only the .exe installer may use the installer launcher")
	}
}

func TestWhatTheUserIsToldAfterTheUpdateStep(t *testing.T) {
	win := installedMessage("phantomops-windows-amd64-installer.exe", "v9.9.9")
	if !strings.Contains(win, "PhantomOps is closing") || !strings.Contains(win, "start PhantomOps again") || !strings.Contains(win, "Start menu") {
		t.Errorf("windows message = %q", win)
	}
	if strings.Contains(win, "choose Yes") || strings.Contains(win, "may ask for permission") {
		t.Errorf("by the time this is shown Windows has already asked for permission; the message must not say it may: %q", win)
	}
	if mac := installedMessage("phantomops-darwin-universal.dmg", "v9.9.9"); !strings.Contains(mac, "installer has been opened") || strings.Contains(mac, "Windows") {
		t.Errorf("macOS message = %q", mac)
	}
	for _, name := range []string{"phantomops-linux-amd64.tar.gz", "phantomops-windows-amd64.zip", "phantomops-linux-amd64.deb"} {
		if m := installedMessage(name, "v9.9.9"); !strings.Contains(m, "v9.9.9") || strings.Contains(m, "installer has been opened") {
			t.Errorf("%s: message = %q, want the in-place update message naming the version", name, m)
		}
	}
}
