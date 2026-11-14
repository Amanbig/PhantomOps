// Package config holds PhantomOps settings: a list of service connections
// plus a few analysis options. It can build the matching collectors.
//
// The file lives at $PHANTOM_CONFIG or <user config dir>/phantomops/config.json
// and is written with 0600 permissions because it may contain tokens.
package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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
	"PhantomOps/internal/llm"
	"PhantomOps/internal/model"
	"PhantomOps/internal/notify"
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

	Socket string `json:"socket"` // docker
	// SSHConnection references a saved SSH connection. A Docker or Kubernetes
	// connection that sets it runs fixed read-only CLI commands on that host
	// instead of locally. Rollbacks are never performed through it.
	SSHConnection string `json:"sshConnection"`
	// RemoteSocket is the Docker socket path on the SSH host; empty uses Docker's default.
	RemoteSocket string `json:"remoteSocket"`
	// RemoteKubeconfig is an absolute kubeconfig path on the SSH host; empty
	// uses kubectl's own lookup there.
	RemoteKubeconfig string `json:"remoteKubeconfig"`
	Context          string `json:"context"` // kubernetes
	// Kubeconfig is an optional kubeconfig file for kubernetes connections.
	// Empty means: $KUBECONFIG, then ~/.kube/config, then kubectl's default.
	Kubeconfig string `json:"kubeconfig"`
	URL        string `json:"url"`   // prometheus, loki, nomad
	Query      string `json:"query"` // loki (LogQL, optional)
	Token      string `json:"token"` // nomad ACL token (secret)

	Addr          string `json:"addr"` // ssh host[:port]
	User          string `json:"user"`
	KeyPath       string `json:"keyPath"`
	KnownHosts    string `json:"knownHosts"`
	Insecure      bool   `json:"insecure"`
	ExpectedPorts []int  `json:"expectedPorts"`
}

// Config is the on-disk settings document.
type Config struct {
	Environment    string            `json:"environment"`
	WindowMinutes  int               `json:"windowMinutes"`
	IgnoreSubjects []string          `json:"ignoreSubjects"`
	Connections    []Connection      `json:"connections"`
	AI             AIConfig          `json:"ai"`
	Remediation    RemediationConfig `json:"remediation"`
	Monitoring     MonitoringConfig  `json:"monitoring"`
	Updates        UpdatesConfig     `json:"updates"`
	// Webhooks are optional chat/JSON destinations for incident messages.
	Webhooks []notify.Channel `json:"webhooks"`
}

// UpdatesConfig controls the update check. Checking only sends an anonymous
// GET to the GitHub Releases API; nothing is installed without the user.
type UpdatesConfig struct {
	// AutoCheckDisabled turns off the automatic check (on by default).
	AutoCheckDisabled bool `json:"autoCheckDisabled"`
}

// MonitoringConfig controls background scans and desktop notifications.
type MonitoringConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"intervalMinutes"`
	Notify          bool `json:"notify"`
	// NotifyMinSeverity is "warning" or "critical" (default).
	NotifyMinSeverity string `json:"notifyMinSeverity"`
}

// Interval returns the scan interval (default 5 minutes).
func (m MonitoringConfig) Interval() time.Duration {
	if m.IntervalMinutes <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(m.IntervalMinutes) * time.Minute
}

// Validate checks the monitoring settings.
func (m MonitoringConfig) Validate() error {
	if m.IntervalMinutes != 0 && (m.IntervalMinutes < 1 || m.IntervalMinutes > 1440) {
		return errors.New("monitoring: interval must be between 1 and 1440 minutes")
	}
	switch m.NotifyMinSeverity {
	case "", "warning", "critical":
		return nil
	}
	return fmt.Errorf("monitoring: unknown severity %q", m.NotifyMinSeverity)
}

// RemediationConfig controls whether PhantomOps may change infrastructure.
// It is off by default: nothing is ever modified until the user opts in.
type RemediationConfig struct {
	Enabled bool `json:"enabled"`
}

// FindConnection returns the enabled connection with the given name.
func (c Config) FindConnection(name string) (Connection, bool) {
	for _, cn := range c.Connections {
		if cn.Enabled && cn.Name == name {
			return cn, true
		}
	}
	return Connection{}, false
}

// AIConfig configures the optional LLM investigator. It speaks the
// OpenAI-compatible chat completions API; Ollama is the "ollama" preset.
type AIConfig struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"` // "ollama" or "openai" (any OpenAI-compatible server)
	BaseURL  string `json:"baseUrl"`  // including version prefix, e.g. http://localhost:11434/v1
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"` // secret; optional for local servers
}

// IsLocalEndpoint reports whether the URL points at this machine or a
// private network, i.e. incident data would not leave the local network.
func IsLocalEndpoint(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// Investigator builds the LLM investigator, or nil when AI is disabled.
func (c Config) Investigator() *llm.Investigator {
	if !c.AI.Enabled || c.AI.Validate() != nil {
		return nil
	}
	return llm.New(llm.NewClient(c.AI.BaseURL, c.AI.Model, c.AI.APIKey))
}

// Validate checks the AI settings (only enforced when enabled).
func (a AIConfig) Validate() error {
	if !a.Enabled {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(a.BaseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("AI: base URL must be a valid http(s) address, e.g. http://localhost:11434/v1")
	}
	if strings.TrimSpace(a.Model) == "" {
		return errors.New("AI: model is required")
	}
	return nil
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
	c.AI.BaseURL = strings.TrimSpace(c.AI.BaseURL)
	c.AI.Model = strings.TrimSpace(c.AI.Model)
	c.AI.APIKey = strings.TrimSpace(c.AI.APIKey)
	if c.AI.Provider == "" {
		c.AI.Provider = "ollama"
	}
	if c.Monitoring.IntervalMinutes == 0 {
		c.Monitoring.IntervalMinutes = 5
	}
	if c.Monitoring.NotifyMinSeverity == "" {
		c.Monitoring.NotifyMinSeverity = "critical"
	}
	for i := range c.Webhooks {
		w := &c.Webhooks[i]
		if w.ID == "" {
			w.ID = NewID()
		}
		w.Name = strings.TrimSpace(w.Name)
		w.URL = strings.TrimSpace(w.URL)
		if w.MinSeverity == "" {
			w.MinSeverity = "critical"
		}
	}
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
		cn.SSHConnection = strings.TrimSpace(cn.SSHConnection)
		cn.RemoteSocket = strings.TrimSpace(cn.RemoteSocket)
		cn.RemoteKubeconfig = strings.TrimSpace(cn.RemoteKubeconfig)
		cn.Context = strings.TrimSpace(cn.Context)
		cn.Kubeconfig = strings.TrimSpace(cn.Kubeconfig)
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
	if err := c.AI.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Monitoring.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	hookNames := map[string]bool{}
	for i, w := range c.Webhooks {
		label := w.Name
		if label == "" {
			label = fmt.Sprintf("webhook #%d", i+1)
		}
		// A masked URL means "unchanged"; it is restored before validation.
		if err := w.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", label, err))
		}
		if n := strings.ToLower(w.Name); n != "" {
			if hookNames[n] {
				problems = append(problems, fmt.Sprintf("%s: another webhook already uses this name", label))
			}
			hookNames[n] = true
		}
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
	for _, cn := range c.Connections {
		if (cn.Type != TypeDocker && cn.Type != TypeKubernetes) || cn.SSHConnection == "" {
			continue
		}
		if cn.Type == TypeDocker && cn.RemoteSocket != "" && !strings.HasPrefix(cn.RemoteSocket, "/") {
			problems = append(problems, fmt.Sprintf("%s: remote Docker socket must be an absolute path", cn.Name))
		}
		if cn.Type == TypeKubernetes && cn.RemoteKubeconfig != "" &&
			(!strings.HasPrefix(cn.RemoteKubeconfig, "/") || strings.ContainsAny(cn.RemoteKubeconfig, "\x00\n\r")) {
			problems = append(problems, fmt.Sprintf("%s: remote kubeconfig must be an absolute path on the SSH host (for example /home/ops/.kube/config; ~ is not expanded)", cn.Name))
		}
		found := false
		for _, sshConn := range c.Connections {
			if sshConn.ID == cn.SSHConnection {
				found = true
				if sshConn.Type != TypeSSH {
					problems = append(problems, fmt.Sprintf("%s: SSH connection reference is not an SSH connection", cn.Name))
				} else if !sshConn.Enabled {
					problems = append(problems, fmt.Sprintf("%s: referenced SSH connection is disabled", cn.Name))
				}
				break
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("%s: SSH connection was not found", cn.Name))
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

// sshHost returns the SSH settings of the connection a Docker or Kubernetes
// connection runs through.
func (c Config) sshHost(cn Connection) (sshc.Config, error) {
	for _, h := range c.Connections {
		if h.ID == cn.SSHConnection && h.Type == TypeSSH {
			return sshc.Config{Addr: h.Addr, User: h.User, KeyPath: h.KeyPath,
				KnownHosts: h.KnownHosts, Insecure: h.Insecure}, nil
		}
	}
	return sshc.Config{}, fmt.Errorf("SSH connection %q was not found", cn.SSHConnection)
}

// CollectorFor builds a connection collector with access to the full config,
// which is needed when a Docker or Kubernetes connection runs through a saved
// SSH connection.
func (c Config) CollectorFor(cn Connection) (collector.Collector, error) {
	if cn.SSHConnection != "" && (cn.Type == TypeDocker || cn.Type == TypeKubernetes) {
		host, err := c.sshHost(cn)
		if err != nil {
			return nil, err
		}
		if cn.Type == TypeDocker {
			return docker.NewRemote(host, cn.RemoteSocket)
		}
		return kubernetes.NewRemote(host, cn.Context, cn.RemoteKubeconfig)
	}
	return cn.Collector()
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
		return kubernetes.NewWithKubeconfig(cn.Context, cn.Kubeconfig), nil
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
		col, err := c.CollectorFor(cn)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", cn.Name, err))
			continue
		}
		cs = append(cs, named{col, cn.Name})
	}
	return cs, notes
}

// named overrides a collector's name with the connection's display name so
// results can be attributed to the right connection in the UI, and stamps
// every event with that origin so later actions know which connection owns it.
type named struct {
	collector.Collector
	name string
}

func (n named) Name() string { return n.name }

func (n named) Collect(ctx context.Context, since time.Time) ([]model.Event, error) {
	evs, err := n.Collector.Collect(ctx, since)
	for i := range evs {
		evs[i].Origin = n.name
	}
	return evs, err
}

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
	if out.AI.APIKey != "" {
		out.AI.APIKey = SecretMask
	}
	out.Webhooks = append([]notify.Channel(nil), c.Webhooks...)
	for i := range out.Webhooks {
		if out.Webhooks[i].URL != "" {
			out.Webhooks[i].URL = SecretMask
		}
	}
	return out
}

// RestoreSecrets replaces masked secrets in c with the values from old
// (matched by connection ID). Masks with no match are cleared.
func (c *Config) RestoreSecrets(old Config) {
	if c.AI.APIKey == SecretMask {
		c.AI.APIKey = old.AI.APIKey
	}
	for i := range c.Webhooks {
		w := &c.Webhooks[i]
		if w.URL != SecretMask {
			continue
		}
		w.URL = ""
		for _, o := range old.Webhooks {
			if o.ID == w.ID {
				w.URL = o.URL
				break
			}
		}
	}
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

// Snapshotter returns the connection's dashboard data source, or ok=false
// when that connection type has no dashboard yet.
// Snapshotter builds the snapshotter for this connection, resolving remote
// Docker connections through their referenced SSH connection.
func (c Config) Snapshotter(cn Connection) (s collector.Snapshotter, ok bool, err error) {
	col, err := c.CollectorFor(cn)
	if err != nil {
		return nil, false, err
	}
	s, ok = col.(collector.Snapshotter)
	return s, ok, nil
}

func (cn Connection) Snapshotter() (s collector.Snapshotter, ok bool, err error) {
	col, err := cn.Collector()
	if err != nil {
		return nil, false, err
	}
	s, ok = col.(collector.Snapshotter)
	return s, ok, nil
}
