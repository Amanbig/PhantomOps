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
