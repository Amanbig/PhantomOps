package ssh

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// testSSHD is a small but real SSH server: real key exchange, real public-key
// authentication, exec requests and direct-tcpip forwarding. It counts login
// attempts so tests can prove that no credential is sent to an unverified
// host.
type testSSHD struct {
	addr string
	ln   net.Listener

	hostKey      atomic.Pointer[gossh.Signer]
	extraKeys    atomic.Pointer[[]gossh.Signer] // more host keys of other types (a real server has several)
	clientPub    gossh.PublicKey
	KeyPath      string       // the matching private key file for Config.KeyPath
	authAttempts atomic.Int32 // public-key attempts
	authRequests atomic.Int32 // every authentication request of any method, including "none"
	logins       atomic.Int32
	conns        atomic.Int32 // TCP connections accepted (one per handshake attempt)

	allowForward atomic.Bool
	execFn       func(cmd string) (stdout, stderr string, code int)
	forwarded    sync.Map // "host:port" asked for
	dialer       func(addr string) (net.Conn, error)
}

func newSigner(t *testing.T) (gossh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

func startSSHD(t *testing.T) *testSSHD {
	t.Helper()
	s := &testSSHD{
		execFn: func(cmd string) (string, string, int) { return "ran:" + cmd, "", 0 },
		dialer: func(addr string) (net.Conn, error) { return net.Dial("tcp", addr) },
	}
	s.allowForward.Store(true)
	host, _ := newSigner(t)
	s.hostKey.Store(&host)

	client, clientPriv := newSigner(t)
	s.clientPub = client.PublicKey()
	block, err := gossh.MarshalPrivateKey(clientPriv, "test")
	if err != nil {
		t.Fatal(err)
	}
	s.KeyPath = filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(s.KeyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln, s.addr = ln, ln.Addr().String()
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.conns.Add(1)
			go s.serve(c)
		}
	}()
	return s
}

func (s *testSSHD) serve(c net.Conn) {
	defer c.Close()
	cfg := &gossh.ServerConfig{
		AuthLogCallback: func(gossh.ConnMetadata, string, error) { s.authRequests.Add(1) },
		PublicKeyCallback: func(_ gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			s.authAttempts.Add(1)
			if bytes.Equal(key.Marshal(), s.clientPub.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unknown key")
		},
	}
	cfg.AddHostKey(*s.hostKey.Load())
	if extra := s.extraKeys.Load(); extra != nil {
		for _, k := range *extra {
			cfg.AddHostKey(k)
		}
	}
	conn, chans, reqs, err := gossh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	s.logins.Add(1)
	go gossh.DiscardRequests(reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			go s.session(nc)
		case "direct-tcpip":
			go s.forward(nc)
		default:
			_ = nc.Reject(gossh.UnknownChannelType, "unsupported")
		}
	}
	conn.Wait()
}

func (s *testSSHD) session(nc gossh.NewChannel) {
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var p struct{ Command string }
		_ = gossh.Unmarshal(req.Payload, &p)
		_ = req.Reply(true, nil)
		out, errOut, code := s.execFn(p.Command)
		_, _ = io.WriteString(ch, out)
		_, _ = io.WriteString(ch.Stderr(), errOut)
		_, _ = ch.SendRequest("exit-status", false, gossh.Marshal(struct{ Status uint32 }{uint32(code)}))
		return
	}
}

func (s *testSSHD) forward(nc gossh.NewChannel) {
	var p struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	_ = gossh.Unmarshal(nc.ExtraData(), &p)
	addr := net.JoinHostPort(p.Host, itoa(p.Port))
	s.forwarded.Store(addr, true)
	if !s.allowForward.Load() {
		_ = nc.Reject(gossh.Prohibited, "open failed")
		return
	}
	target, err := s.dialer(addr)
	if err != nil {
		_ = nc.Reject(gossh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		target.Close()
		return
	}
	go gossh.DiscardRequests(reqs)
	go func() { _, _ = io.Copy(target, ch); target.Close() }()
	go func() { _, _ = io.Copy(ch, target); ch.Close() }()
}

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// config returns a verified Config pointing at the server, with known_hosts
// in a fresh temp dir (not created yet).
func (s *testSSHD) config(t *testing.T) Config {
	t.Helper()
	return Config{Addr: s.addr, User: "ops", KeyPath: s.KeyPath, KnownHosts: filepath.Join(t.TempDir(), "ssh", "known_hosts")}
}

func (s *testSSHD) hostFingerprint() string {
	return gossh.FingerprintSHA256((*s.hostKey.Load()).PublicKey())
}

// newECDSASigner returns a fresh ECDSA P-256 host key.
func newECDSASigner(t *testing.T) gossh.Signer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sg, err := gossh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return sg
}

var (
	rsaOnce   sync.Once
	rsaSigner gossh.Signer
)

// newRSASigner returns an RSA host key (generated once: it is slow).
func newRSASigner(t *testing.T) gossh.Signer {
	t.Helper()
	rsaOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		if rsaSigner, err = gossh.NewSignerFromKey(k); err != nil {
			t.Fatal(err)
		}
	})
	return rsaSigner
}

// offerSeveralKeys makes the server offer ed25519 (its main key), ECDSA and
// RSA, like a real OpenSSH server.
func (s *testSSHD) offerSeveralKeys(t *testing.T) (ed, ec, rs gossh.Signer) {
	t.Helper()
	ed, ec, rs = *s.hostKey.Load(), newECDSASigner(t), newRSASigner(t)
	extra := []gossh.Signer{ec, rs}
	s.extraKeys.Store(&extra)
	return ed, ec, rs
}
