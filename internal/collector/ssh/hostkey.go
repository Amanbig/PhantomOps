package ssh

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Host key status values.
const (
	// HostKeyTrusted: the server's key is in known_hosts.
	HostKeyTrusted = "trusted"
	// HostKeyUnknown: the server is not in known_hosts yet.
	HostKeyUnknown = "unknown"
	// HostKeyChanged: known_hosts has a different key for this server.
	HostKeyChanged = "changed"
)

// HostKeyInfo describes the key a server presented.
type HostKeyInfo struct {
	Addr           string `json:"addr"`
	KeyType        string `json:"keyType"`
	Fingerprint    string `json:"fingerprint"` // SHA256:..., like ssh-keygen -l
	Status         string `json:"status"`
	KnownHostsPath string `json:"knownHostsPath"`
	// Known lists the fingerprints known_hosts has for this server when the
	// status is "changed".
	Known []string `json:"known"`
}

// expandHome expands a leading ~ the way a shell would.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

func knownHostsPath(cfg Config) (string, error) {
	if cfg.KnownHosts != "" {
		return expandHome(cfg.KnownHosts), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// loadKnownHosts returns the verifier for path. A missing file means no host
// is trusted yet; it is not an error.
func loadKnownHosts(path string) (gossh.HostKeyCallback, error) {
	cb, err := knownhosts.New(path)
	if errors.Is(err, fs.ErrNotExist) {
		return func(string, net.Addr, gossh.PublicKey) error { return &knownhosts.KeyError{} }, nil
	}
	if err != nil {
		return nil, fmt.Errorf("known_hosts %s: %w", path, err)
	}
	return cb, nil
}

// describeKeyError turns a known_hosts failure into advice, keeping the
// underlying error for errors.As.
func describeKeyError(addr string, key gossh.PublicKey, err error) error {
	var ke *knownhosts.KeyError
	if !errors.As(err, &ke) {
		return err
	}
	if len(ke.Want) == 0 {
		return fmt.Errorf("the host key of %s is not trusted yet (%s). Open this SSH connection and use \"Check host key\" to compare and trust it: %w",
			addr, gossh.FingerprintSHA256(key), err)
	}
	return fmt.Errorf("the host key of %s CHANGED (now %s). This can mean the server was reinstalled, or that someone is intercepting the connection. "+
		"PhantomOps will not connect. Verify the new fingerprint with the server's owner, then remove the old entry from known_hosts yourself: %w",
		addr, gossh.FingerprintSHA256(key), err)
}

// errKeyCaptured aborts a handshake right after the host key was received,
// before any authentication is attempted.
var errKeyCaptured = errors.New("host key captured")

// probe connects, reads the server's host key and classifies it against
// known_hosts. It never authenticates: the handshake is aborted as soon as
// the key arrives, so no credential is sent to a server that has not been
// verified.
func probe(ctx context.Context, cfg Config) (gossh.PublicKey, HostKeyInfo, error) {
	path, err := knownHostsPath(cfg)
	if err != nil {
		return nil, HostKeyInfo{}, err
	}
	verify, err := loadKnownHosts(path)
	if err != nil {
		return nil, HostKeyInfo{}, err
	}

	var key gossh.PublicKey
	info := HostKeyInfo{Addr: cfg.Addr, KnownHostsPath: path}
	cc := &gossh.ClientConfig{
		User:    cfg.User,
		Timeout: 10 * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, k gossh.PublicKey) error {
			key = k
			info.KeyType, info.Fingerprint = k.Type(), gossh.FingerprintSHA256(k)
			verr := verify(hostname, remote, k)
			var ke *knownhosts.KeyError
			switch {
			case verr == nil:
				info.Status = HostKeyTrusted
			case !errors.As(verr, &ke):
				return verr // e.g. a revoked key: report it as an error
			case len(ke.Want) == 0:
				info.Status = HostKeyUnknown
			default:
				info.Status = HostKeyChanged
				for _, w := range ke.Want {
					info.Known = append(info.Known, gossh.FingerprintSHA256(w.Key))
				}
			}
			return errKeyCaptured // abort before authentication
		},
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", cfg.Addr)
	if err != nil {
		return nil, HostKeyInfo{}, err
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() { // honour cancellation during the handshake
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	// The callback always aborts, so this only ever returns an error.
	if c, _, _, err := gossh.NewClientConn(conn, cfg.Addr, cc); err == nil {
		c.Close()
	}
	if key == nil || info.Status == "" {
		if err := ctx.Err(); err != nil {
			return nil, HostKeyInfo{}, err
		}
		return nil, HostKeyInfo{}, fmt.Errorf("could not read the host key of %s (is that an SSH server?)", cfg.Addr)
	}
	return key, info, nil
}

// InspectHostKey reports the server's host key and whether known_hosts
// trusts it. It sends no credentials.
func InspectHostKey(ctx context.Context, cfg Config) (HostKeyInfo, error) {
	cfg, err := normalize(cfg)
	if err != nil {
		return HostKeyInfo{}, err
	}
	_, info, err := probe(ctx, cfg)
	return info, err
}

// TrustHostKey adds the server's host key to known_hosts, but only if the key
// it presents now has exactly the fingerprint the user confirmed, and only
// for a server that is not yet known. It never replaces or removes an entry:
// a changed key is refused, because it may mean the connection is being
// intercepted.
func TrustHostKey(ctx context.Context, cfg Config, confirmed string) (HostKeyInfo, error) {
	if cfg.Insecure {
		return HostKeyInfo{}, errors.New("host key verification is switched off for this connection, so there is nothing to trust")
	}
	if !strings.HasPrefix(confirmed, "SHA256:") {
		return HostKeyInfo{}, errors.New("the confirmed fingerprint is missing; check the host key first")
	}
	cfg, err := normalize(cfg)
	if err != nil {
		return HostKeyInfo{}, err
	}
	key, info, err := probe(ctx, cfg)
	if err != nil {
		return HostKeyInfo{}, err
	}
	switch info.Status {
	case HostKeyTrusted:
		return info, nil
	case HostKeyChanged:
		return info, fmt.Errorf("the host key of %s is already in %s with a different fingerprint (%s). PhantomOps will not replace it: that could hide an interception. Verify the new key with the server's owner and remove the old line yourself",
			cfg.Addr, info.KnownHostsPath, strings.Join(info.Known, ", "))
	}
	if info.Fingerprint != confirmed {
		return info, fmt.Errorf("the server now presents %s, not the %s you confirmed. Nothing was saved; check the host key again", info.Fingerprint, confirmed)
	}
	if err := appendKnownHost(info.KnownHostsPath, knownhosts.Line([]string{knownhosts.Normalize(cfg.Addr)}, key)); err != nil {
		return info, fmt.Errorf("could not save the host key: %w", err)
	}
	info.Status = HostKeyTrusted
	return info, nil
}

var knownHostsMu sync.Mutex

// appendKnownHost appends one line to path, creating the file (0600) and its
// folder (0700) if needed. Existing lines are never touched.
func appendKnownHost(path, line string) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			prefix = "\n" // never glue the new entry onto an unterminated line
		}
	}
	if _, err := f.WriteString(prefix + strings.TrimRight(line, "\n") + "\n"); err != nil {
		return err
	}
	return f.Sync()
}
