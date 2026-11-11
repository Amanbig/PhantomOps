// Package config loads PhantomOps settings and builds the matching collectors.
//
// The file lives at $PHANTOM_CONFIG or <user config dir>/phantomops/config.json.
// If it does not exist, Docker and Kubernetes are auto-detected and everything
// else stays off.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"PhantomOps/internal/collector"
	"PhantomOps/internal/collector/docker"
	"PhantomOps/internal/collector/kubernetes"
	"PhantomOps/internal/collector/loki"
	"PhantomOps/internal/collector/nomad"
	"PhantomOps/internal/collector/prometheus"
	sshc "PhantomOps/internal/collector/ssh"
	"PhantomOps/internal/engine"
)

// Config is the on-disk settings document.
type Config struct {
	Environment    string   `json:"environment"`
	WindowMinutes  int      `json:"windowMinutes"`
	IgnoreSubjects []string `json:"ignoreSubjects"`

	Docker     DockerCfg     `json:"docker"`
	Kubernetes KubernetesCfg `json:"kubernetes"`
	Prometheus URLCfg        `json:"prometheus"`
	Loki       LokiCfg       `json:"loki"`
	Nomad      NomadCfg      `json:"nomad"`
	SSH        []SSHCfg      `json:"ssh"`
}

// DockerCfg configures the Docker collector. Enabled nil means auto-detect.
type DockerCfg struct {
	Enabled *bool  `json:"enabled"`
	Socket  string `json:"socket"`
}

// KubernetesCfg configures the Kubernetes collector. Enabled nil means
// auto-detect (kubectl on PATH).
type KubernetesCfg struct {
	Enabled *bool  `json:"enabled"`
	Context string `json:"context"`
}

// URLCfg is a collector identified by a base URL; empty URL disables it.
type URLCfg struct {
	URL string `json:"url"`
}

// LokiCfg configures the Loki collector.
type LokiCfg struct {
	URL   string `json:"url"`
	Query string `json:"query"`
}

// NomadCfg configures the Nomad collector.
type NomadCfg struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// SSHCfg is one SSH target.
type SSHCfg struct {
	Addr          string `json:"addr"`
	User          string `json:"user"`
	KeyPath       string `json:"keyPath"`
	KnownHosts    string `json:"knownHosts"`
	Insecure      bool   `json:"insecure"`
	ExpectedPorts []int  `json:"expectedPorts"`
}

// Path returns the config file location.
func Path() string {
	if p := os.Getenv("PHANTOM_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "phantomops", "config.json")
}

// Load reads the config file. A missing file yields the zero Config.
func Load(path string) (Config, error) {
	var c Config
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// Collectors builds the enabled collectors. Problems with individual
// entries are returned as notes rather than failing everything.
func (c Config) Collectors() ([]collector.Collector, []string) {
	var cs []collector.Collector
	var notes []string

	socket := c.Docker.Socket
	if socket == "" {
		socket = docker.DefaultSocket
	}
	if enabled(c.Docker.Enabled, fileExists(socket)) {
		cs = append(cs, docker.New(socket))
	}
	if enabled(c.Kubernetes.Enabled, onPath("kubectl")) {
		cs = append(cs, kubernetes.New(c.Kubernetes.Context))
	}
	if c.Prometheus.URL != "" {
		cs = append(cs, prometheus.New(c.Prometheus.URL, nil))
	}
	if c.Loki.URL != "" {
		cs = append(cs, loki.New(c.Loki.URL, c.Loki.Query))
	}
	if c.Nomad.URL != "" {
		cs = append(cs, nomad.New(c.Nomad.URL, c.Nomad.Token))
	}
	for _, h := range c.SSH {
		sc, err := sshc.New(sshc.Config{Addr: h.Addr, User: h.User, KeyPath: h.KeyPath,
			KnownHosts: h.KnownHosts, Insecure: h.Insecure, ExpectedPorts: h.ExpectedPorts})
		if err != nil {
			notes = append(notes, fmt.Sprintf("ssh %q: %v", h.Addr, err))
			continue
		}
		cs = append(cs, sc)
	}
	return cs, notes
}

// Engine builds a ready-to-use correlation engine from the config.
func (c Config) Engine() (*engine.Engine, []string) {
	cs, notes := c.Collectors()
	window := time.Duration(c.WindowMinutes) * time.Minute
	return engine.New(engine.Config{
		Collectors:     cs,
		Window:         window,
		Environment:    c.Environment,
		IgnoreSubjects: c.IgnoreSubjects,
	}), notes
}

func enabled(explicit *bool, auto bool) bool {
	if explicit != nil {
		return *explicit
	}
	return auto
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func onPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
