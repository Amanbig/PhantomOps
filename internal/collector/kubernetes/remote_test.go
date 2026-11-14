package kubernetes

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRemote struct {
	mu       sync.Mutex
	commands []string
	stdout   string
	stderr   string
	code     int
	err      error
}

func (f *fakeRemote) Run(_ context.Context, cmd string) (string, string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	return f.stdout, f.stderr, f.code, f.err
}

func TestRemoteRunsExactlyTheRequestedReadOnlyCommand(t *testing.T) {
	f := &fakeRemote{stdout: `{"items":[]}`}
	run := remoteRunnerFunc(f, "prod", "/home/ops/.kube/config")
	out, err := run(context.Background(), "get", "pods", "-A", "-o", "json")
	if err != nil || string(out) != `{"items":[]}` {
		t.Fatalf("out=%q err=%v", out, err)
	}
	want := `KUBECONFIG='/home/ops/.kube/config' 'kubectl' '--context' 'prod' 'get' 'pods' '-A' '-o' 'json'`
	if len(f.commands) != 1 || f.commands[0] != want {
		t.Errorf("command = %q\nwant      %q", f.commands, want)
	}
}

func TestRemoteWithoutContextOrKubeconfigUsesKubectlDefaults(t *testing.T) {
	f := &fakeRemote{stdout: "{}"}
	_, err := remoteRunnerFunc(f, "", "")(context.Background(), "version", "-o", "json")
	if err != nil || f.commands[0] != `'kubectl' 'version' '-o' 'json'` {
		t.Errorf("err=%v command=%q", err, f.commands)
	}
}

func TestRemoteRefusesAnythingNotOnTheAllowlistWithoutContactingTheHost(t *testing.T) {
	f := &fakeRemote{stdout: "{}"}
	run := remoteRunnerFunc(f, "", "")
	for _, args := range [][]string{
		{"delete", "pod", "x"},
		{"exec", "-it", "x", "--", "sh"},
		{"apply", "-f", "-"},
		{"rollout", "undo", "deployment/api"},
		{"get", "secrets", "-A", "-o", "json"},
		{"logs", "pod-x"},
		{"get", "pods", "-A", "-o", "yaml"},
		{"get", "pods", "-A", "-o", "json", "; rm -rf /"},
		{"get", "--raw", "/api/v1/namespaces/kube-system/secrets"},
		{"config", "view", "--raw"},
		{},
	} {
		if _, err := run(context.Background(), args...); err == nil || !strings.Contains(err.Error(), "allowlist") {
			t.Errorf("kubectl %v was not refused: %v", args, err)
		}
	}
	if len(f.commands) != 0 {
		t.Errorf("refused commands reached the remote host: %v", f.commands)
	}
}

func TestRemoteQuotesHostileValues(t *testing.T) {
	f := &fakeRemote{stdout: "{}"}
	_, _ = remoteRunnerFunc(f, `a'; touch /tmp/pwned; '`, `/tmp/it's here/config`)(context.Background(), "version", "-o", "json")
	got := f.commands[0]
	if !strings.Contains(got, `'--context' 'a'"'"'; touch /tmp/pwned; '"'"''`) || !strings.Contains(got, `KUBECONFIG='/tmp/it'"'"'s here/config'`) {
		t.Errorf("values were not quoted safely: %s", got)
	}
}

func TestRemoteExplainsCommonFailures(t *testing.T) {
	cases := []struct {
		name   string
		f      *fakeRemote
		expect string
	}{
		{"kubectl missing", &fakeRemote{code: 127, stderr: "bash: kubectl: command not found"}, "not found on the remote host"},
		{"k3s root-only kubeconfig", &fakeRemote{code: 1, stderr: "error: error loading config file \"/etc/rancher/k3s/k3s.yaml\": open /etc/rancher/k3s/k3s.yaml: permission denied"}, "~/.kube/config"},
		{"plain failure", &fakeRemote{code: 1, stderr: "Error from server (Forbidden): pods is forbidden"}, "Forbidden"},
		{"empty stderr falls back to stdout", &fakeRemote{code: 2, stdout: "something on stdout"}, "something on stdout"},
	}
	for _, c := range cases {
		_, err := remoteRunnerFunc(c.f, "", "")(context.Background(), "get", "nodes", "-o", "json")
		if err == nil || !strings.Contains(err.Error(), c.expect) || !strings.Contains(err.Error(), "exited") {
			t.Errorf("%s: err=%v, want it to mention %q", c.name, err, c.expect)
		}
	}
	if _, err := remoteRunnerFunc(&fakeRemote{err: errors.New("connection reset")}, "", "")(context.Background(), "get", "nodes", "-o", "json"); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("transport error lost: %v", err)
	}
}

// Every kubectl call the collector and the dashboard really make must be on
// the remote allowlist; otherwise a remote cluster would silently lose data.
// This test fails if someone adds a call to the collector without deciding
// whether it is safe to run remotely.
func TestEveryCallTheCollectorMakesIsOnTheRemoteAllowlist(t *testing.T) {
	var mu sync.Mutex
	var seen [][]string
	rec := func(_ context.Context, args ...string) ([]byte, error) {
		mu.Lock()
		seen = append(seen, append([]string(nil), args...))
		mu.Unlock()
		return []byte("{}"), nil
	}
	c := NewWithRunner(rec)
	if _, err := c.Collect(context.Background(), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(seen) < 8 {
		t.Fatalf("only %d kubectl calls were recorded; the test is not exercising the collector", len(seen))
	}
	for _, args := range seen {
		if !remoteAllowed[strings.Join(args, "\x00")] {
			t.Errorf("the collector runs `kubectl %s`, which is not on the remote allowlist", strings.Join(args, " "))
		}
	}
}

func TestRemoteCollectorEndToEndWithFakeHost(t *testing.T) {
	f := &fakeRemote{stdout: `{"items":[]}`}
	c := NewWithRunner(remoteRunnerFunc(f, "", ""))
	events, err := c.Collect(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("events=%v err=%v", events, err)
	}
	for _, cmd := range f.commands {
		if !strings.HasPrefix(cmd, `'kubectl' `) {
			t.Errorf("unexpected remote command %q", cmd)
		}
	}
}
