package ssh

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

func ctx5(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestInspectingAnUnknownHostSendsNoCredentialsAndWritesNothing(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t)
	info, err := InspectHostKey(ctx5(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != HostKeyUnknown || info.Fingerprint != s.hostFingerprint() || info.KeyType != "ssh-ed25519" {
		t.Errorf("info = %+v, want an unknown ed25519 key with the server's fingerprint %s", info, s.hostFingerprint())
	}
	if s.authRequests.Load() != 0 || s.authAttempts.Load() != 0 || s.logins.Load() != 0 {
		t.Errorf("auth requests=%d key attempts=%d logins=%d: the handshake must stop before any authentication with a host nobody verified",
			s.authRequests.Load(), s.authAttempts.Load(), s.logins.Load())
	}
	if _, err := os.Stat(cfg.KnownHosts); !os.IsNotExist(err) {
		t.Errorf("inspecting must not create or change known_hosts (stat err=%v)", err)
	}
}

func TestTrustSavesExactlyTheConfirmedKeyAndThenConnectionsWork(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t)
	info, _ := InspectHostKey(ctx5(t), cfg)

	// Before trusting, a real connection is refused with advice, and still
	// sends no credential.
	h, _ := Shared(cfg)
	t.Cleanup(h.Close)
	_, _, _, err := h.Run(ctx5(t), "uptime")
	if err == nil || !strings.Contains(err.Error(), "not trusted yet") || !strings.Contains(err.Error(), info.Fingerprint) {
		t.Fatalf("err = %v, want advice that names the fingerprint", err)
	}
	if s.authRequests.Load() != 0 {
		t.Fatalf("authentication started (%d requests) before the host was trusted", s.authRequests.Load())
	}

	got, err := TrustHostKey(ctx5(t), cfg, info.Fingerprint)
	if err != nil || got.Status != HostKeyTrusted {
		t.Fatalf("trust: %+v err=%v", got, err)
	}
	if again, _ := InspectHostKey(ctx5(t), cfg); again.Status != HostKeyTrusted {
		t.Errorf("after trusting, status = %q", again.Status)
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{cfg.KnownHosts: 0o600, filepath.Dir(cfg.KnownHosts): 0o700} {
			if st, err := os.Stat(path); err != nil || st.Mode().Perm() != want {
				t.Errorf("%s mode = %v err=%v, want %v", path, st.Mode().Perm(), err, want)
			}
		}
	}

	// The real handshake, authentication and exec path.
	out, _, code, err := h.Run(ctx5(t), "uptime")
	if err != nil || code != 0 || out != "ran:uptime" {
		t.Fatalf("out=%q code=%d err=%v", out, code, err)
	}
	if s.logins.Load() != 1 {
		t.Errorf("%d logins, want 1", s.logins.Load())
	}
}

func TestTrustRefusesAWrongOrMissingFingerprintAndSavesNothing(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t)
	for name, confirmed := range map[string]string{
		"different key": "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"empty":         "",
		"no prefix":     strings.TrimPrefix(s.hostFingerprint(), "SHA256:"),
	} {
		if _, err := TrustHostKey(ctx5(t), cfg, confirmed); err == nil {
			t.Errorf("%s: trust succeeded", name)
		}
	}
	if _, err := os.Stat(cfg.KnownHosts); !os.IsNotExist(err) {
		t.Errorf("a refused trust wrote known_hosts (stat err=%v)", err)
	}
}

func TestTrustRefusesWhenTheKeyChangedBetweenLookingAndTrusting(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t)
	seen, _ := InspectHostKey(ctx5(t), cfg) // the user looked at this fingerprint

	other, _ := newSigner(t) // ... then a different key shows up
	s.hostKey.Store(&other)

	_, err := TrustHostKey(ctx5(t), cfg, seen.Fingerprint)
	if err == nil || !strings.Contains(err.Error(), "not the") {
		t.Fatalf("err = %v, want a refusal naming both fingerprints", err)
	}
	if _, serr := os.Stat(cfg.KnownHosts); !os.IsNotExist(serr) {
		t.Error("a key that was never confirmed was saved")
	}
}

func TestAChangedHostKeyIsReportedAndNeverReplaced(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t)
	first, _ := InspectHostKey(ctx5(t), cfg)
	if _, err := TrustHostKey(ctx5(t), cfg, first.Fingerprint); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cfg.KnownHosts)

	other, _ := newSigner(t) // the server's key changes (reinstall, or an attacker)
	s.hostKey.Store(&other)

	info, err := InspectHostKey(ctx5(t), cfg)
	if err != nil || info.Status != HostKeyChanged || len(info.Known) != 1 || info.Known[0] != first.Fingerprint {
		t.Fatalf("info = %+v err=%v, want status changed naming the old fingerprint", info, err)
	}
	if _, err := TrustHostKey(ctx5(t), cfg, info.Fingerprint); err == nil || !strings.Contains(err.Error(), "will not replace") {
		t.Errorf("trusting a changed key must be refused, got %v", err)
	}
	after, _ := os.ReadFile(cfg.KnownHosts)
	if string(after) != string(before) {
		t.Errorf("known_hosts was modified:\nbefore %q\nafter  %q", before, after)
	}

	// And a normal connection fails closed with a clear warning.
	logins := s.authRequests.Load()
	h, _ := Shared(cfg)
	t.Cleanup(h.Close)
	_, _, _, err = h.Run(ctx5(t), "uptime")
	if err == nil || !strings.Contains(err.Error(), "CHANGED") || !strings.Contains(err.Error(), "will not connect") {
		t.Errorf("err = %v, want a loud warning", err)
	}
	if s.authRequests.Load() != logins {
		t.Error("authentication started with a server whose key changed")
	}
}

func TestTrustingAnAlreadyTrustedHostDoesNotDuplicateTheEntry(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t)
	info, _ := InspectHostKey(ctx5(t), cfg)
	for i := 0; i < 3; i++ {
		if _, err := TrustHostKey(ctx5(t), cfg, info.Fingerprint); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(cfg.KnownHosts)
	if n := strings.Count(strings.TrimSpace(string(b)), "\n") + 1; n != 1 {
		t.Errorf("known_hosts has %d lines after trusting 3 times, want 1:\n%s", n, b)
	}
}

func TestTrustKeepsExistingEntriesAndNeverGluesOntoAnUnterminatedLine(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t)
	_ = os.MkdirAll(filepath.Dir(cfg.KnownHosts), 0o700)
	other, _ := newSigner(t)
	existing := "other.example " + string(gossh.MarshalAuthorizedKey(other.PublicKey()))
	existing = strings.TrimRight(existing, "\n") // no trailing newline, as editors sometimes leave it
	if err := os.WriteFile(cfg.KnownHosts, []byte("# my hosts\n"+existing), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := InspectHostKey(ctx5(t), cfg)
	if info.Status != HostKeyUnknown {
		t.Fatalf("status = %q", info.Status)
	}
	if _, err := TrustHostKey(ctx5(t), cfg, info.Fingerprint); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg.KnownHosts)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[0] != "# my hosts" || lines[1] != existing {
		t.Errorf("existing entries were damaged:\n%s", b)
	}
	if again, _ := InspectHostKey(ctx5(t), cfg); again.Status != HostKeyTrusted {
		t.Errorf("status after trust = %q", again.Status)
	}
}

func TestTrustIsRefusedWhenVerificationIsSwitchedOff(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t)
	cfg.Insecure = true
	if _, err := TrustHostKey(ctx5(t), cfg, s.hostFingerprint()); err == nil {
		t.Error("trusting must not be offered for a connection that skips verification")
	}
}

func TestKnownHostsPathExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for in, want := range map[string]string{
		"~/.ssh/kh": filepath.Join(home, ".ssh", "kh"),
		"~":         home,
		"":          filepath.Join(home, ".ssh", "known_hosts"),
		"/abs/kh":   "/abs/kh",
		"rel/kh":    "rel/kh",
		"~other/x":  "~other/x", // another user's home is not supported, left as is
		"a~/b":      "a~/b",
	} {
		got, err := knownHostsPath(Config{KnownHosts: in})
		if err != nil || got != want {
			t.Errorf("knownHostsPath(%q) = %q err=%v, want %q", in, got, err, want)
		}
	}
}

func TestAMissingKnownHostsFileMeansNothingIsTrustedNotAnError(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t) // the file and its folder do not exist
	info, err := InspectHostKey(ctx5(t), cfg)
	if err != nil || info.Status != HostKeyUnknown {
		t.Errorf("info=%+v err=%v", info, err)
	}
}

func TestInspectDoesNotHangOrCrashOnThingsThatAreNotSSH(t *testing.T) {
	cases := map[string]func(net.Conn){
		"closes at once": func(c net.Conn) { c.Close() },
		"speaks HTTP":    func(c net.Conn) { _, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n"); c.Close() },
		"garbage banner": func(c net.Conn) { _, _ = io.WriteString(c, "SSH-9.9-nonsense\r\n\x00\x01\x02"); c.Close() },
		"never answers":  func(c net.Conn) { time.Sleep(3 * time.Second); c.Close() },
	}
	for name, behave := range cases {
		t.Run(name, func(t *testing.T) {
			ln, _ := net.Listen("tcp", "127.0.0.1:0")
			defer ln.Close()
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					go behave(c)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := InspectHostKey(ctx, Config{Addr: ln.Addr().String(), User: "u", KnownHosts: filepath.Join(t.TempDir(), "kh")})
			if err == nil {
				t.Error("expected an error")
			}
			if time.Since(start) > 2*time.Second {
				t.Errorf("took %v: cancellation was not honoured", time.Since(start))
			}
		})
	}
}

func TestInspectReportsAnUnreachableHost(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if _, err := InspectHostKey(ctx5(t), Config{Addr: addr, User: "u", KnownHosts: filepath.Join(t.TempDir(), "kh")}); err == nil {
		t.Error("expected a connection error")
	}
	if _, err := InspectHostKey(ctx5(t), Config{Addr: "", User: "u"}); err == nil {
		t.Error("a missing address must be rejected")
	}
}

// --- the real connect / exec / forward paths, against a real SSH server -------

func trustedHost(t *testing.T, s *testSSHD) (*Host, Config) {
	t.Helper()
	cfg := s.config(t)
	info, err := InspectHostKey(ctx5(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TrustHostKey(ctx5(t), cfg, info.Fingerprint); err != nil {
		t.Fatal(err)
	}
	h, err := Shared(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h, cfg
}

func TestRealSSHExecCapturesStdoutStderrAndExitCode(t *testing.T) {
	s := startSSHD(t)
	s.execFn = func(cmd string) (string, string, int) { return "to-stdout", "to-stderr", 3 }
	h, _ := trustedHost(t, s)
	out, errOut, code, err := h.Run(ctx5(t), "anything")
	if err != nil || out != "to-stdout" || errOut != "to-stderr" || code != 3 {
		t.Errorf("out=%q err=%q code=%d err=%v", out, errOut, code, err)
	}
}

func TestRealSSHRefusesAnUnauthorisedKey(t *testing.T) {
	s := startSSHD(t)
	cfg := s.config(t)
	info, _ := InspectHostKey(ctx5(t), cfg)
	_, _ = TrustHostKey(ctx5(t), cfg, info.Fingerprint)
	_, wrongPriv := newSigner(t)
	block, _ := gossh.MarshalPrivateKey(wrongPriv, "x")
	cfg.KeyPath = filepath.Join(t.TempDir(), "wrong")
	_ = os.WriteFile(cfg.KeyPath, pem.EncodeToMemory(block), 0o600)
	h, _ := Shared(cfg)
	t.Cleanup(h.Close)
	if _, _, _, err := h.Run(ctx5(t), "x"); err == nil {
		t.Error("a key the server does not know must be refused")
	}
}

func TestRealSSHTunnelCarriesHTTPAndStaysReadOnly(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "from-backend "+r.Method)
	}))
	t.Cleanup(backend.Close)
	s := startSSHD(t)
	h, _ := trustedHost(t, s)
	client := &http.Client{Transport: HTTPTransport(h), Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)

	resp, err := client.Get(backend.URL + "/v1/jobs")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "from-backend GET" {
		t.Errorf("body = %q", body)
	}
	if _, found := s.forwarded.Load(strings.TrimPrefix(backend.URL, "http://")); !found {
		t.Error("the SSH server was never asked to open the URL's address")
	}
	if _, err := client.Post(backend.URL, "text/plain", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("POST through a real tunnel: err=%v", err)
	}
	if s.logins.Load() != 1 {
		t.Errorf("%d SSH logins for a command-free tunnel with 2 requests, want 1", s.logins.Load())
	}
}

func TestRealSSHReportsForwardingBeingDisabledAndKeepsTheLinkUsable(t *testing.T) {
	s := startSSHD(t)
	s.allowForward.Store(false)
	h, _ := trustedHost(t, s)
	client := &http.Client{Transport: HTTPTransport(h), Timeout: 5 * time.Second}
	_, err := client.Get("http://127.0.0.1:4646/")
	if err == nil || !errors.Is(err, ErrForwardingDisabled) && !strings.Contains(err.Error(), "does not allow port forwarding") {
		t.Fatalf("err = %v, want the port-forwarding explanation", err)
	}
	if out, _, _, err := h.Run(ctx5(t), "still"); err != nil || out != "ran:still" {
		t.Errorf("commands must keep working when only forwarding is off: out=%q err=%v", out, err)
	}
	if s.logins.Load() != 1 {
		t.Errorf("%d logins: a policy refusal must not reconnect", s.logins.Load())
	}
}

func TestRealSSHDistinguishesAServiceThatIsNotListening(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close() // nothing listens there any more
	s := startSSHD(t)
	h, _ := trustedHost(t, s)
	client := &http.Client{Transport: HTTPTransport(h), Timeout: 5 * time.Second}
	_, err := client.Get("http://" + dead + "/")
	if err == nil || !strings.Contains(err.Error(), "could not connect to "+dead) {
		t.Fatalf("err = %v, want the SSH host could not connect to the service", err)
	}
	if errors.Is(err, ErrForwardingDisabled) {
		t.Error("a closed port must not be reported as forwarding being disabled")
	}
	if s.logins.Load() != 1 {
		t.Errorf("%d logins: an unreachable service must not reconnect SSH", s.logins.Load())
	}
}
