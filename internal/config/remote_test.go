package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"PhantomOps/internal/collector"
)

func TestRemoteDockerReferencesAnSSHConnection(t *testing.T) {
	cfg := Config{Connections: []Connection{
		{ID: "ssh1", Type: TypeSSH, Name: "edge", Enabled: true, Addr: "edge.example:22", User: "ops"},
		{ID: "docker1", Type: TypeDocker, Name: "edge-docker", SSHConnection: "ssh1", RemoteSocket: "/var/run/docker.sock"},
	}}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid remote Docker config rejected: %v", err)
	}
	col, err := cfg.CollectorFor(cfg.Connections[1])
	if err != nil || col == nil {
		t.Fatalf("collector=%v err=%v", col, err)
	}
}

func TestRemoteDockerReferenceValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"missing SSH", Config{Connections: []Connection{{ID: "d", Type: TypeDocker, Name: "d", SSHConnection: "missing"}}}, "SSH connection was not found"},
		{"wrong type", Config{Connections: []Connection{{ID: "d", Type: TypeDocker, Name: "d", SSHConnection: "p"}, {ID: "p", Type: TypePrometheus, Name: "p", URL: "http://127.0.0.1:9090"}}}, "not an SSH connection"},
		{"relative socket", Config{Connections: []Connection{{ID: "s", Type: TypeSSH, Name: "s", Addr: "x", User: "u"}, {ID: "d", Type: TypeDocker, Name: "d", SSHConnection: "s", RemoteSocket: "relative.sock"}}}, "absolute path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.Normalize()
			err := tc.cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err=%v want %q", err, tc.want)
			}
		})
	}
}

func TestRemoteKubernetesReferencesAnSSHConnection(t *testing.T) {
	cfg := Config{Connections: []Connection{
		{ID: "ssh1", Type: TypeSSH, Name: "edge", Enabled: true, Addr: "edge.example:22", User: "ops"},
		{ID: "k1", Type: TypeKubernetes, Name: "edge-k3s", Enabled: true, SSHConnection: "ssh1", Context: "default", RemoteKubeconfig: "/home/ops/.kube/config"},
	}}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid remote Kubernetes config rejected: %v", err)
	}
	col, err := cfg.CollectorFor(cfg.Connections[1])
	if err != nil || col == nil || col.Name() != "kubernetes" {
		t.Fatalf("collector=%v err=%v", col, err)
	}
	if _, ok, err := cfg.Snapshotter(cfg.Connections[1]); err != nil || !ok {
		t.Errorf("a remote Kubernetes connection must still have a dashboard: ok=%v err=%v", ok, err)
	}
}

func TestRemoteKubernetesValidation(t *testing.T) {
	ssh := Connection{ID: "s", Type: TypeSSH, Name: "s", Enabled: true, Addr: "x", User: "u"}
	k8s := func(mut func(*Connection)) Config {
		k := Connection{ID: "k", Type: TypeKubernetes, Name: "k", Enabled: true, SSHConnection: "s"}
		mut(&k)
		return Config{Connections: []Connection{ssh, k}}
	}
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"relative kubeconfig", k8s(func(c *Connection) { c.RemoteKubeconfig = ".kube/config" }), "absolute path"},
		{"tilde is not expanded", k8s(func(c *Connection) { c.RemoteKubeconfig = "~/.kube/config" }), "absolute path"},
		{"newline in path", k8s(func(c *Connection) { c.RemoteKubeconfig = "/a\nb" }), "absolute path"},
		{"missing SSH", k8s(func(c *Connection) { c.SSHConnection = "nope" }), "was not found"},
		{"SSH disabled", Config{Connections: []Connection{{ID: "s", Type: TypeSSH, Name: "s", Addr: "x", User: "u"}, {ID: "k", Type: TypeKubernetes, Name: "k", Enabled: true, SSHConnection: "s"}}}, "disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.Normalize()
			if err := tc.cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err=%v want %q", err, tc.want)
			}
		})
	}
}

func TestDirectKubernetesAndDockerAreUnaffected(t *testing.T) {
	cfg := Config{Connections: []Connection{
		{ID: "k", Type: TypeKubernetes, Name: "local", Enabled: true, RemoteKubeconfig: "ignored-without-ssh"},
		{ID: "d", Type: TypeDocker, Name: "dock", Enabled: true},
	}}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("direct connections must not be validated as remote ones: %v", err)
	}
}

func TestHTTPServicesCanRunThroughAnSSHConnection(t *testing.T) {
	ssh := Connection{ID: "s", Type: TypeSSH, Name: "edge", Enabled: true, Addr: "edge.example:22", User: "ops"}
	cases := map[string]Connection{
		"nomad":      {ID: "n", Type: TypeNomad, Name: "edge-nomad", Enabled: true, URL: "http://127.0.0.1:4646", Token: "tok", SSHConnection: "s"},
		"prometheus": {ID: "p", Type: TypePrometheus, Name: "edge-prom", Enabled: true, URL: "http://127.0.0.1:9090", SSHConnection: "s"},
		"loki":       {ID: "l", Type: TypeLoki, Name: "edge-loki", Enabled: true, URL: "http://127.0.0.1:3100", SSHConnection: "s"},
	}
	for name, cn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Config{Connections: []Connection{ssh, cn}}
			cfg.Normalize()
			if err := cfg.Validate(); err != nil {
				t.Fatalf("valid config rejected: %v", err)
			}
			col, err := cfg.CollectorFor(cfg.Connections[1])
			if err != nil || col == nil || col.Name() != name {
				t.Fatalf("collector=%v err=%v", col, err)
			}
			if _, ok, err := cfg.Snapshotter(cfg.Connections[1]); err != nil || !ok {
				t.Errorf("a tunnelled %s connection must still have a dashboard: ok=%v err=%v", name, ok, err)
			}
		})
	}
}

func TestHTTPServiceSSHReferenceValidation(t *testing.T) {
	enabledSSH := Connection{ID: "s", Type: TypeSSH, Name: "s", Enabled: true, Addr: "x", User: "u"}
	prom := func(mut func(*Connection)) Connection {
		c := Connection{ID: "p", Type: TypePrometheus, Name: "p", Enabled: true, URL: "http://127.0.0.1:9090", SSHConnection: "s"}
		mut(&c)
		return c
	}
	cases := []struct {
		name  string
		conns []Connection
		want  string
	}{
		{"missing SSH", []Connection{prom(func(c *Connection) { c.SSHConnection = "nope" })}, "was not found"},
		{"wrong type", []Connection{{ID: "s", Type: TypeLoki, Name: "s", URL: "http://x:1"}, prom(func(c *Connection) {})}, "not an SSH connection"},
		{"disabled SSH", []Connection{{ID: "s", Type: TypeSSH, Name: "s", Addr: "x", User: "u"}, prom(func(c *Connection) {})}, "disabled"},
		{"bad URL still rejected", []Connection{enabledSSH, prom(func(c *Connection) { c.URL = "not a url" })}, "URL must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Connections: tc.conns}
			cfg.Normalize()
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err=%v want %q", err, tc.want)
			}
		})
	}
}

func TestTunnelledNomadTokenStaysMasked(t *testing.T) {
	cfg := Config{Connections: []Connection{
		{ID: "s", Type: TypeSSH, Name: "s", Enabled: true, Addr: "x", User: "u"},
		{ID: "n", Type: TypeNomad, Name: "n", Enabled: true, URL: "http://127.0.0.1:4646", Token: "tok", SSHConnection: "s"},
	}}
	ui := cfg.Redacted()
	if ui.Connections[1].Token != SecretMask || ui.Connections[1].SSHConnection != "s" {
		t.Errorf("token must be masked and the SSH reference kept: %+v", ui.Connections[1])
	}
	ui.RestoreSecrets(cfg)
	if ui.Connections[1].Token != "tok" {
		t.Errorf("token not restored: %q", ui.Connections[1].Token)
	}
}

func TestSSHReferenceIsIgnoredForTypesThatCannotUseIt(t *testing.T) {
	cfg := Config{Connections: []Connection{{ID: "s", Type: TypeSSH, Name: "s", Enabled: true, Addr: "x", User: "u", SSHConnection: "whatever"}}}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Errorf("a stray sshConnection on an SSH connection must not break validation: %v", err)
	}
	if runsThroughSSH(Connection{Type: TypeSSH}) {
		t.Error("an SSH connection does not run through another SSH connection")
	}
}

// An SSH-backed connection must fail closed. If the SSH host cannot be
// reached, PhantomOps must not quietly connect to the URL directly from this
// computer: a URL like http://127.0.0.1:4646 means the *remote* machine's
// service, and a local service on the same port would be the wrong one.
func TestSSHBackedHTTPConnectionsNeverFallBackToADirectConnection(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	var hits atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	defer local.Close()

	for _, typ := range []string{TypeNomad, TypePrometheus, TypeLoki} {
		t.Run(typ, func(t *testing.T) {
			cfg := Config{Connections: []Connection{
				{ID: "s", Type: TypeSSH, Name: "unreachable", Enabled: true, Addr: "127.0.0.1:1", User: "ops"},
				{ID: "c", Type: typ, Name: "svc", Enabled: true, URL: local.URL, SSHConnection: "s"},
			}}
			cfg.Normalize()
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			col, err := cfg.CollectorFor(cfg.Connections[1])
			if err != nil {
				return // refusing to build it is also fail-closed
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = col.Collect(ctx, time.Now().Add(-time.Hour))
			if s, ok := col.(collector.Snapshotter); ok {
				_, _ = s.Snapshot(ctx)
			}
			if hits.Load() != 0 {
				t.Fatalf("%s: the service on this computer received %d request(s) although the connection is set to run through SSH", typ, hits.Load())
			}
		})
	}
}
