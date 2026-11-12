package kubeconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func home(t *testing.T, withConfig bool) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	if withConfig {
		if err := os.MkdirAll(filepath.Join(h, ".kube"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h, ".kube", "config"), []byte("apiVersion: v1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestResolveOrder(t *testing.T) {
	h := home(t, true)
	t.Setenv("KUBECONFIG", "")

	// connection setting wins over everything, and ~ is expanded
	if p, src := Resolve("~/custom.yaml"); p != filepath.Join(h, "custom.yaml") || src != SourceConnection {
		t.Errorf("explicit: %q %v", p, src)
	}
	// then $KUBECONFIG
	t.Setenv("KUBECONFIG", "/somewhere/cfg")
	if p, src := Resolve(""); p != "/somewhere/cfg" || src != SourceEnv {
		t.Errorf("env: %q %v", p, src)
	}
	// then ~/.kube/config
	t.Setenv("KUBECONFIG", "")
	if p, src := Resolve(""); p != filepath.Join(h, ".kube", "config") || src != SourceHome {
		t.Errorf("home: %q %v", p, src)
	}
	// otherwise leave it to kubectl
	home(t, false)
	if p, src := Resolve(""); p != "" || src != SourceDefault {
		t.Errorf("default: %q %v", p, src)
	}
}

func TestCommandEnv(t *testing.T) {
	h := home(t, true)
	t.Setenv("KUBECONFIG", "")
	build := func() *exec.Cmd { return exec.Command("true") }

	// no env var + ~/.kube/config -> injected, as when started from the desktop
	cmd := Command("", build)
	want := "KUBECONFIG=" + filepath.Join(h, ".kube", "config")
	if !contains(cmd.Env, want) {
		t.Errorf("not injected: %v", cmd.Env)
	}
	// a set $KUBECONFIG is left alone (it may be a list of files)
	t.Setenv("KUBECONFIG", "/a:/b")
	if cmd := Command("", build); cmd.Env != nil {
		t.Errorf("env var should be inherited untouched, got %v", cmd.Env)
	}
	// an explicit path overrides $KUBECONFIG
	if cmd := Command("/x/y", build); !contains(cmd.Env, "KUBECONFIG=/x/y") {
		t.Errorf("explicit not applied")
	}
	// nothing to inject
	t.Setenv("KUBECONFIG", "")
	home(t, false)
	if cmd := Command("", build); cmd.Env != nil {
		t.Errorf("nothing resolved, env should be inherited: %v", cmd.Env)
	}
}

func contains(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestCheck(t *testing.T) {
	h := home(t, true)
	if Check("") != nil {
		t.Error("empty path is fine")
	}
	if Check("~/.kube/config") != nil {
		t.Error("existing file rejected")
	}
	for path, want := range map[string]string{
		filepath.Join(h, "missing"): "does not exist",
		filepath.Join(h, ".kube"):   "folder",
	} {
		if err := Check(path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Check(%s) = %v, want %q", path, err, want)
		}
	}
	locked := filepath.Join(h, "locked")
	os.WriteFile(locked, []byte("x"), 0o000)
	if err := Check(locked); err == nil {
		t.Skip("running as root: permission checks do not apply")
	} else if !strings.Contains(err.Error(), "permission") && !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("locked file: %v", err)
	}
}

func TestExplain(t *testing.T) {
	k3s := `time="2026-11-12T17:11:46+05:30" level=warning msg="Unable to read /etc/rancher/k3s/k3s.yaml, please start server with --write-kubeconfig-mode"
error: error loading config file "/etc/rancher/k3s/k3s.yaml": open /etc/rancher/k3s/k3s.yaml: permission denied`
	for in, want := range map[string]string{
		k3s: "~/.kube/config",
		`Get "http://localhost:8080/api": dial tcp 127.0.0.1:8080: connect: connection refused`: "localhost:8080",
		"error: no configuration has been provided":                                             "no kubeconfig",
		"Error from server (NotFound): deployments.apps \"x\" not found":                        "NotFound",
	} {
		if got := Explain(in); !strings.Contains(got, want) {
			t.Errorf("Explain(%.40q) = %q, want it to contain %q", in, got, want)
		}
	}
	if got := Explain("Error from server (NotFound): x"); strings.Contains(got, "kubeconfig") {
		t.Errorf("unrelated errors must pass through untouched: %q", got)
	}
}
