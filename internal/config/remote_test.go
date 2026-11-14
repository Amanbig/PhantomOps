package config

import (
	"strings"
	"testing"
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
