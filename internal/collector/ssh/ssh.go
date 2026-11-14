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
	// dialFull is dial with stderr, used by CommandRunner.
	dialFull func(ctx context.Context) (execFull, func(), error)
}

// execFull is Exec that also returns stderr.
type execFull func(ctx context.Context, cmd string) (stdout, stderr string, exitCode int, err error)

// Output limits, so a runaway command cannot exhaust memory.
const (
	maxStdout = 64 << 20
	maxStderr = 64 << 10
)

// limitedBuffer is a bytes.Buffer that refuses to grow past max.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		return 0, fmt.Errorf("command output is larger than %d MB", l.max>>20)
	}
	return l.buf.Write(p)
}

// normalize checks the required fields and adds the default port.
func normalize(cfg Config) (Config, error) {
	if cfg.Addr == "" || cfg.User == "" {
		return cfg, errors.New("ssh: host and user are required")
	}
	if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		cfg.Addr = net.JoinHostPort(cfg.Addr, "22")
	}
	return cfg, nil
}

// New creates an SSH collector.
func New(cfg Config) (*Collector, error) {
	cfg, err := normalize(cfg)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(cfg.Addr)
	c := &Collector{cfg: cfg, host: host, now: func() time.Time { return time.Now().UTC() }}
	c.dialFull = c.dialSSHFull
	c.dial = func(ctx context.Context) (Exec, func(), error) {
		full, closeFn, err := c.dialFull(ctx)
		if err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context, cmd string) (string, int, error) {
			out, _, code, err := full(ctx, cmd)
			return out, code, err
		}, closeFn, nil
	}
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

func (c *Collector) dialSSHFull(ctx context.Context) (execFull, func(), error) {
	l, err := connect(ctx, c.cfg)
	if err != nil {
		return nil, nil, err
	}
	return l.exec, l.close, nil
}

// sshLink is one authenticated, host-key-verified SSH connection. It can run
// commands and open TCP forwards.
type sshLink struct {
	client    *gossh.Client
	closeAuth func()
}

// connect dials and authenticates. cfg must already be normalized.
func connect(ctx context.Context, cfg Config) (*sshLink, error) {
	auth, closeAuth, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}
	hostKey, err := hostKeyCallback(cfg)
	if err != nil {
		closeAuth()
		return nil, err
	}
	cc := &gossh.ClientConfig{User: cfg.User, Auth: auth, HostKeyCallback: hostKey, Timeout: 10 * time.Second}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", cfg.Addr)
	if err != nil {
		closeAuth()
		return nil, err
	}
	sc, chans, reqs, err := gossh.NewClientConn(conn, cfg.Addr, cc)
	if err != nil {
		conn.Close()
		closeAuth()
		return nil, err
	}
	return &sshLink{client: gossh.NewClient(sc, chans, reqs), closeAuth: closeAuth}, nil
}

func (l *sshLink) close() { l.client.Close(); l.closeAuth() }

func (l *sshLink) exec(ctx context.Context, cmd string) (string, string, int, error) {
	sess, err := l.client.NewSession()
	if err != nil {
		return "", "", 0, err
	}
	defer sess.Close()
	stdout, stderr := &limitedBuffer{max: maxStdout}, &limitedBuffer{max: maxStderr}
	sess.Stdout, sess.Stderr = stdout, stderr
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case <-ctx.Done():
		sess.Close()
		return "", "", 0, ctx.Err()
	case err := <-done:
		var ee *gossh.ExitError
		if errors.As(err, &ee) {
			return stdout.buf.String(), stderr.buf.String(), ee.ExitStatus(), nil
		}
		return stdout.buf.String(), stderr.buf.String(), 0, err
	}
}

// ErrForwardingDisabled means the SSH server refused to open a TCP forward.
var ErrForwardingDisabled = errors.New("the SSH server does not allow port forwarding for this user (AllowTcpForwarding is off, or the user is restricted); " +
	"use a user that may forward, or monitor that service another way")

// dialTCP asks the SSH server to connect to addr and returns that stream.
func (l *sshLink) dialTCP(ctx context.Context, addr string) (net.Conn, error) {
	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := l.client.Dial("tcp", addr)
		ch <- result{c, err}
	}()
	select {
	case <-ctx.Done():
		go func() { // do not leak a stream that opened after we gave up
			if r := <-ch; r.c != nil {
				r.c.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		return r.c, classifyForwardError(r.err, addr)
	}
}

func classifyForwardError(err error, addr string) error {
	var oe *gossh.OpenChannelError
	if errors.As(err, &oe) {
		switch oe.Reason {
		case gossh.Prohibited:
			return ErrForwardingDisabled
		case gossh.ConnectionFailed:
			return &remoteRefused{addr: addr, msg: oe.Message}
		}
		return &remoteRefused{addr: addr, msg: oe.Message}
	}
	return err
}

// remoteRefused means the SSH server was reachable and healthy but could not
// open the requested connection (for example nothing listens on that port).
type remoteRefused struct{ addr, msg string }

func (e *remoteRefused) Error() string {
	return fmt.Sprintf("the SSH host could not connect to %s: %s", e.addr, e.msg)
}

func authMethods(cfg Config) ([]gossh.AuthMethod, func(), error) {
	if cfg.KeyPath != "" {
		pem, err := os.ReadFile(cfg.KeyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read key: %w", err)
		}
		signer, err := gossh.ParsePrivateKey(pem)
		if err != nil {
			return nil, nil, fmt.Errorf("parse key %s (encrypted keys are not supported; use ssh-agent): %w", cfg.KeyPath, err)
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

func hostKeyCallback(cfg Config) (gossh.HostKeyCallback, error) {
	if cfg.Insecure {
		return gossh.InsecureIgnoreHostKey(), nil //nolint:gosec // explicit opt-in
	}
	path := cfg.KnownHosts
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
