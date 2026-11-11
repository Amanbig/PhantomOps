// Package config holds PhantomOps settings: a list of service connections
// plus a few analysis options. It can build the matching collectors.
//
// The file lives at $PHANTOM_CONFIG or <user config dir>/phantomops/config.json
// and is written with 0600 permissions because it may contain tokens.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Connection types.
const (
	TypeDocker     = "docker"
	TypeKubernetes = "kubernetes"
	TypePrometheus = "prometheus"
	TypeLoki       = "loki"
	TypeNomad      = "nomad"
	TypeSSH        = "ssh"
)

// SecretMask replaces secrets when settings are sent to the UI.
const SecretMask = "********"

// Connection is one connected service. Only the fields relevant to its Type
// are used.
type Connection struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`

	Socket  string `json:"socket"`  // docker
	Context string `json:"context"` // kubernetes
	URL     string `json:"url"`     // prometheus, loki, nomad
	Query   string `json:"query"`   // loki (LogQL, optional)
	Token   string `json:"token"`   // nomad ACL token (secret)

	Addr          string `json:"addr"` // ssh host[:port]
	User          string `json:"user"`
	KeyPath       string `json:"keyPath"`
	KnownHosts    string `json:"knownHosts"`
	Insecure      bool   `json:"insecure"`
	ExpectedPorts []int  `json:"expectedPorts"`
}

// Config is the on-disk settings document.
type Config struct {
	Environment    string       `json:"environment"`
	WindowMinutes  int          `json:"windowMinutes"`
	IgnoreSubjects []string     `json:"ignoreSubjects"`
	Connections    []Connection `json:"connections"`
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

// NewID returns a random connection ID.
func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Load reads the config file. saved is false when the file does not exist;
// in that case the returned config holds auto-detected Docker/Kubernetes
// connections so PhantomOps works out of the box.
func Load(path string) (cfg Config, saved bool, err error) {
	if path != "" {
		b, rerr := os.ReadFile(path)
		switch {
		case rerr == nil:
			if err := json.Unmarshal(b, &cfg); err != nil {
				return Config{}, false, fmt.Errorf("parse %s: %w", path, err)
			}
			return cfg, true, nil
		case !errors.Is(rerr, os.ErrNotExist):
			return Config{}, false, rerr
		}
	}
	return Defaults(), false, nil
}

// Defaults returns the configuration used before anything is saved.
func Defaults() Config {
	cfg := Config{WindowMinutes: 30}
	if fileExists(docker.DefaultSocket) {
		cfg.Connections = append(cfg.Connections, Connection{
			ID: NewID(), Type: TypeDocker, Name: "Local Docker", Enabled: true, Socket: docker.DefaultSocket})
	}
	if onPath("kubectl") {
		cfg.Connections = append(cfg.Connections, Connection{
			ID: NewID(), Type: TypeKubernetes, Name: "Kubernetes (current context)", Enabled: true})
	}
	return cfg
}

// Save validates and atomically writes the config with 0600 permissions.
func Save(path string, cfg Config) error {
	if path == "" {
		return errors.New("no config path available")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Normalize fills in missing IDs and trims whitespace.
func (c *Config) Normalize() {
	c.Environment = strings.TrimSpace(c.Environment)
	if c.WindowMinutes <= 0 {
		c.WindowMinutes = 30
	}
	var ignore []string
	for _, s := range c.IgnoreSubjects {
		if s = strings.TrimSpace(s); s != "" {
			ignore = append(ignore, s)
		}
	}
	c.IgnoreSubjects = ignore
	for i := range c.Connections {
		cn := &c.Connections[i]
		if cn.ID == "" {
			cn.ID = NewID()
		}
		cn.Name = strings.TrimSpace(cn.Name)
		cn.URL = strings.TrimSpace(cn.URL)
		cn.Addr = strings.TrimSpace(cn.Addr)
		cn.User = strings.TrimSpace(cn.User)
		cn.Socket = strings.TrimSpace(cn.Socket)
		cn.Context = strings.TrimSpace(cn.Context)
		cn.KeyPath = strings.TrimSpace(cn.KeyPath)
		cn.KnownHosts = strings.TrimSpace(cn.KnownHosts)
	}
}

// Validate checks every connection and returns all problems at once.
func (c Config) Validate() error {
	var problems []string
	if c.WindowMinutes < 0 || c.WindowMinutes > 24*60 {
		problems = append(problems, "window must be between 1 and 1440 minutes")
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	for i, cn := range c.Connections {
		label := cn.Name
		if label == "" {
			label = fmt.Sprintf("connection #%d", i+1)
		}
		if err := cn.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", label, err))
		}
		if n := strings.ToLower(strings.TrimSpace(cn.Name)); n != "" {
			if names[n] {
				problems = append(problems, fmt.Sprintf("%s: another connection already uses this name", label))
			}
			names[n] = true
		}
		if cn.ID != "" {
			if seen[cn.ID] {
				problems = append(problems, fmt.Sprintf("%s: duplicate id", label))
			}
			seen[cn.ID] = true
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// Validate checks a single connection.
func (cn Connection) Validate() error {
	if strings.TrimSpace(cn.Name) == "" {
		return errors.New("name is required")
	}
	switch cn.Type {
	case TypeDocker, TypeKubernetes:
		return nil
	case TypePrometheus, TypeLoki, TypeNomad:
		u, err := url.Parse(strings.TrimSpace(cn.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("URL must be a valid http(s) address, e.g. http://localhost:9090")
		}
		return nil
	case TypeSSH:
		if strings.TrimSpace(cn.Addr) == "" || strings.TrimSpace(cn.User) == "" {
			return errors.New("host and user are required")
		}
		for _, p := range cn.ExpectedPorts {
			if p < 1 || p > 65535 {
				return fmt.Errorf("invalid port %d", p)
			}
		}
		return nil
	}
	return fmt.Errorf("unknown connection type %q", cn.Type)
}

// Collector builds the collector for this connection.
func (cn Connection) Collector() (collector.Collector, error) {
	if err := cn.Validate(); err != nil {
		return nil, err
	}
	switch cn.Type {
	case TypeDocker:
		socket := cn.Socket
		if socket == "" {
			socket = docker.DefaultSocket
		}
		return docker.New(socket), nil
	case TypeKubernetes:
		return kubernetes.New(cn.Context), nil
	case TypePrometheus:
		return prometheus.New(cn.URL, nil), nil
	case TypeLoki:
		return loki.New(cn.URL, cn.Query), nil
	case TypeNomad:
		return nomad.New(cn.URL, cn.Token), nil
	case TypeSSH:
		return sshc.New(sshc.Config{Addr: cn.Addr, User: cn.User, KeyPath: cn.KeyPath,
			KnownHosts: cn.KnownHosts, Insecure: cn.Insecure, ExpectedPorts: cn.ExpectedPorts})
	}
	return nil, fmt.Errorf("unknown connection type %q", cn.Type)
}

// Collectors builds the enabled collectors. Problems with individual
// connections are returned as notes rather than failing everything.
func (c Config) Collectors() ([]collector.Collector, []string) {
	var cs []collector.Collector
	var notes []string
	for _, cn := range c.Connections {
		if !cn.Enabled {
			continue
		}
		col, err := cn.Collector()
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", cn.Name, err))
			continue
		}
		cs = append(cs, named{col, cn.Name})
	}
	return cs, notes
}

// named overrides a collector's name with the connection's display name so
// results can be attributed to the right connection in the UI.
type named struct {
	collector.Collector
	name string
}

func (n named) Name() string { return n.name }

// Engine builds a ready-to-use correlation engine from the config.
func (c Config) Engine() (*engine.Engine, []string) {
	cs, notes := c.Collectors()
	return engine.New(engine.Config{
		Collectors:     cs,
		Window:         time.Duration(c.WindowMinutes) * time.Minute,
		Environment:    c.Environment,
		IgnoreSubjects: c.IgnoreSubjects,
	}), notes
}

// Redacted returns a copy with secrets masked, safe to send to the UI.
func (c Config) Redacted() Config {
	out := c
	out.Connections = append([]Connection(nil), c.Connections...)
	for i := range out.Connections {
		if out.Connections[i].Token != "" {
			out.Connections[i].Token = SecretMask
		}
	}
	return out
}

// RestoreSecrets replaces masked secrets in c with the values from old
// (matched by connection ID). Masks with no match are cleared.
func (c *Config) RestoreSecrets(old Config) {
	for i := range c.Connections {
		cn := &c.Connections[i]
		if cn.Token != SecretMask {
			continue
		}
		cn.Token = ""
		for _, o := range old.Connections {
			if o.ID == cn.ID {
				cn.Token = o.Token
				break
			}
		}
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func onPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
