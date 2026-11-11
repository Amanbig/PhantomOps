// Package ssh runs a fixed set of read-only diagnostic commands on a remote
// Linux host over SSH and converts the output into events.
//
// Security: only the built-in allowlisted commands are ever executed; callers
// cannot supply arbitrary commands. Host keys are verified against
// known_hosts unless Insecure is explicitly set.
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"PhantomOps/internal/model"
)

// Config describes how to reach and check a host.
type Config struct {
	Addr string // host:port (port defaults to 22)
	User string
	// KeyPath is an unencrypted private key. If empty, the SSH agent
	// ($SSH_AUTH_SOCK) is used.
	KeyPath string
	// KnownHosts is the known_hosts file (default ~/.ssh/known_hosts).
	KnownHosts string
	// Insecure disables host key verification. Never use in production.
	Insecure bool
	// ExpectedPorts are TCP ports that should be listening on the host.
	ExpectedPorts []int
}

// Exec runs a command on the host and returns its stdout and exit status.
// It is a seam for tests.
type Exec func(ctx context.Context, cmd string) (stdout string, exitCode int, err error)

// Collector implements collector.Collector for SSH.
type Collector struct {
	cfg  Config
	host string
	now  func() time.Time

	// dial opens an Exec session; replaceable in tests.
	dial func(ctx context.Context) (Exec, func(), error)
}

// New creates an SSH collector.
func New(cfg Config) (*Collector, error) {
	if cfg.Addr == "" || cfg.User == "" {
		return nil, errors.New("ssh: host and user are required")
	}
	if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		cfg.Addr = net.JoinHostPort(cfg.Addr, "22")
	}
	host, _, _ := net.SplitHostPort(cfg.Addr)
	c := &Collector{cfg: cfg, host: host, now: func() time.Time { return time.Now().UTC() }}
	c.dial = c.dialSSH
	return c, nil
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return "ssh" }

// The allowlist. Every command is read-only.
const (
	cmdLoad  = "cat /proc/loadavg; nproc"
	cmdMem   = "grep -E '^(MemTotal|MemAvailable|SwapTotal|SwapFree):' /proc/meminfo"
	cmdDisk  = "df -P"
	cmdUnits = "systemctl --failed --no-legend --plain"
	cmdOOM   = "dmesg 2>/dev/null | grep -iE 'out of memory|oom-kill|killed process' | tail -5"
	cmdPorts = "ss -H -ltn 2>/dev/null || netstat -ltn"
)

// Collect implements collector.Collector. Checks reflect the host's current
// state, so `since` is ignored.
func (c *Collector) Collect(ctx context.Context, _ time.Time) ([]model.Event, error) {
	run, closeFn, err := c.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s: %w", c.cfg.Addr, err)
	}
	defer closeFn()

	var events []model.Event
	check := func(name, cmd string, parse func(string) []model.Event) {
		out, code, err := run(ctx, cmd)
		switch {
		case err != nil:
			events = append(events, c.info("Partial data", fmt.Sprintf("%s: %v", name, err)))
		case code != 0 && out == "":
			// Tool missing or unsupported on this host (e.g. no systemd): skip quietly.
		default:
			events = append(events, parse(out)...)
		}
	}

	check("load", cmdLoad, c.parseLoad)
	check("memory", cmdMem, c.parseMemory)
	check("disk", cmdDisk, c.parseDisk)
	check("systemd", cmdUnits, c.parseFailedUnits)
	check("kernel log", cmdOOM, c.parseOOM)
	if len(c.cfg.ExpectedPorts) > 0 {
		check("ports", cmdPorts, c.parsePorts)
	}
	return events, nil
}

func (c *Collector) event(kind model.Kind, sev model.Severity, title, detail string) model.Event {
	return model.Event{Time: c.now(), Source: model.SourceSSH, Kind: kind, Severity: sev,
		Subject: c.host, Title: title, Detail: detail}
}

func (c *Collector) info(title, detail string) model.Event {
	return c.event(model.KindOther, model.SeverityInfo, title, detail)
}

func (c *Collector) dialSSH(ctx context.Context) (Exec, func(), error) {
	auth, closeAuth, err := c.authMethods()
	if err != nil {
		return nil, nil, err
	}
	hostKey, err := c.hostKeyCallback()
	if err != nil {
		closeAuth()
		return nil, nil, err
	}
	cc := &gossh.ClientConfig{User: c.cfg.User, Auth: auth, HostKeyCallback: hostKey, Timeout: 10 * time.Second}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.cfg.Addr)
	if err != nil {
		closeAuth()
		return nil, nil, err
	}
	sc, chans, reqs, err := gossh.NewClientConn(conn, c.cfg.Addr, cc)
	if err != nil {
		conn.Close()
		closeAuth()
		return nil, nil, err
	}
	client := gossh.NewClient(sc, chans, reqs)
	closeFn := func() { client.Close(); closeAuth() }

	run := func(ctx context.Context, cmd string) (string, int, error) {
		sess, err := client.NewSession()
		if err != nil {
			return "", 0, err
		}
		defer sess.Close()
		var stdout bytes.Buffer
		sess.Stdout = &stdout
		done := make(chan error, 1)
		go func() { done <- sess.Run(cmd) }()
		select {
		case <-ctx.Done():
			sess.Close()
			return "", 0, ctx.Err()
		case err := <-done:
			var ee *gossh.ExitError
			if errors.As(err, &ee) {
				return stdout.String(), ee.ExitStatus(), nil
			}
			return stdout.String(), 0, err
		}
	}
	return run, closeFn, nil
}

func (c *Collector) authMethods() ([]gossh.AuthMethod, func(), error) {
	if c.cfg.KeyPath != "" {
		pem, err := os.ReadFile(c.cfg.KeyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read key: %w", err)
		}
		signer, err := gossh.ParsePrivateKey(pem)
		if err != nil {
			return nil, nil, fmt.Errorf("parse key %s (encrypted keys are not supported; use ssh-agent): %w", c.cfg.KeyPath, err)
		}
		return []gossh.AuthMethod{gossh.PublicKeys(signer)}, func() {}, nil
	}
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, errors.New("no key given and SSH_AUTH_SOCK is not set")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, fmt.Errorf("connect ssh-agent: %w", err)
	}
	return []gossh.AuthMethod{gossh.PublicKeysCallback(agent.NewClient(conn).Signers)}, func() { conn.Close() }, nil
}

func (c *Collector) hostKeyCallback() (gossh.HostKeyCallback, error) {
	if c.cfg.Insecure {
		return gossh.InsecureIgnoreHostKey(), nil //nolint:gosec // explicit opt-in
	}
	path := c.cfg.KnownHosts
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("known_hosts %s: %w (add the host with ssh-keyscan, or enable insecure mode for testing)", path, err)
	}
	return cb, nil
}
