package ssh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// writeKnownHosts stores exactly these keys for the server's address, the way
// the ssh command would.
func writeKnownHosts(t *testing.T, cfg Config, keys ...gossh.PublicKey) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(cfg.KnownHosts), 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(knownhosts.Line([]string{knownhosts.Normalize(cfg.Addr)}, k) + "\n")
	}
	if err := os.WriteFile(cfg.KnownHosts, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The bug this file guards against, found by testing against a real OpenSSH
// server: with only the ed25519 key in known_hosts (what a normal ssh login
// stores), PhantomOps negotiated the server's ECDSA key and reported a
// changed host key although nothing had changed.
func TestKnownHostsWithOneKeyTypeMatchesAServerThatOffersSeveral(t *testing.T) {
	for name, pick := range map[string]func(ed, ec, rs gossh.Signer) gossh.Signer{
		"ed25519": func(ed, ec, rs gossh.Signer) gossh.Signer { return ed },
		"ecdsa":   func(ed, ec, rs gossh.Signer) gossh.Signer { return ec },
		"rsa":     func(ed, ec, rs gossh.Signer) gossh.Signer { return rs },
	} {
		t.Run(name, func(t *testing.T) {
			s := startSSHD(t)
			known := pick(s.offerSeveralKeys(t))
			cfg := s.config(t)
			writeKnownHosts(t, cfg, known.PublicKey())

			info, err := InspectHostKey(ctx5(t), cfg)
			if err != nil || info.Status != HostKeyTrusted || info.KeyType != known.PublicKey().Type() {
				t.Fatalf("inspect: %+v err=%v, want trusted via the %s key", info, err, known.PublicKey().Type())
			}
			h, _ := Shared(cfg)
			t.Cleanup(h.Close)
			out, _, _, err := h.Run(ctx5(t), "uptime")
			if err != nil || out != "ran:uptime" {
				t.Fatalf("a real connection must work, got out=%q err=%v", out, err)
			}
		})
	}
}

func TestAServerThatIsNotKnownYetShowsItsEd25519KeyFirst(t *testing.T) {
	s := startSSHD(t)
	ed, _, _ := s.offerSeveralKeys(t)
	info, err := InspectHostKey(ctx5(t), s.config(t))
	if err != nil || info.Status != HostKeyUnknown || info.KeyType != "ssh-ed25519" || info.Fingerprint != gossh.FingerprintSHA256(ed.PublicKey()) {
		t.Errorf("info=%+v err=%v: the key to compare should be the ed25519 one (it is what OpenSSH users expect, and what the on-screen advice names)", info, err)
	}
}

func TestTrustingSavesTheKeyThatWasShownAndLaterConnectionsMatchIt(t *testing.T) {
	s := startSSHD(t)
	s.offerSeveralKeys(t)
	cfg := s.config(t)
	shown, _ := InspectHostKey(ctx5(t), cfg)
	if _, err := TrustHostKey(ctx5(t), cfg, shown.Fingerprint); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg.KnownHosts)
	if n := strings.Count(string(b), "ssh-"); n != 1 || !strings.Contains(string(b), "ssh-ed25519") {
		t.Errorf("exactly the shown ed25519 key must be saved:\n%s", b)
	}
	h, _ := Shared(cfg)
	t.Cleanup(h.Close)
	if out, _, _, err := h.Run(ctx5(t), "x"); err != nil || out != "ran:x" {
		t.Errorf("out=%q err=%v", out, err)
	}
}

// Security: asking for the key type known_hosts has must never turn into
// "trust whatever else the server shows".
func TestAChangedKeyOfAKnownTypeIsStillRefusedEvenIfOtherTypesAreOffered(t *testing.T) {
	for name, tc := range map[string]struct{ knownType string }{
		"ed25519 replaced": {"ed25519"},
		"ecdsa replaced":   {"ecdsa"},
	} {
		t.Run(name, func(t *testing.T) {
			s := startSSHD(t)
			ed, ec, rs := s.offerSeveralKeys(t)
			cfg := s.config(t)
			oldKey := ed
			if tc.knownType == "ecdsa" {
				oldKey = ec
			}
			writeKnownHosts(t, cfg, oldKey.PublicKey())

			// The server's key of that type changes; it still offers the others.
			newEd, newEc := ed, ec
			if tc.knownType == "ed25519" {
				newEd, _ = newSigner(t)
			} else {
				newEc = newECDSASigner(t)
			}
			s.hostKey.Store(&newEd)
			extra := []gossh.Signer{newEc, rs}
			s.extraKeys.Store(&extra)

			info, err := InspectHostKey(ctx5(t), cfg)
			if err != nil || info.Status != HostKeyChanged || len(info.Known) != 1 || info.Known[0] != gossh.FingerprintSHA256(oldKey.PublicKey()) {
				t.Fatalf("inspect: %+v err=%v, want changed, naming the old fingerprint", info, err)
			}
			h, _ := Shared(cfg)
			t.Cleanup(h.Close)
			_, _, _, err = h.Run(ctx5(t), "echo should-not-run")
			if err == nil || !strings.Contains(err.Error(), "CHANGED") {
				t.Fatalf("err=%v, want a refusal that says the key changed", err)
			}
			if s.authRequests.Load() != 0 {
				t.Errorf("authentication started (%d requests) with a server whose key changed", s.authRequests.Load())
			}
			if _, err := TrustHostKey(ctx5(t), cfg, info.Fingerprint); err == nil {
				t.Error("trusting must not replace the old key")
			}
		})
	}
}

func TestSeveralKnownTypesAreAllAccepted(t *testing.T) {
	s := startSSHD(t)
	ed, ec, _ := s.offerSeveralKeys(t)
	cfg := s.config(t)
	writeKnownHosts(t, cfg, ed.PublicKey(), ec.PublicKey())
	info, err := InspectHostKey(ctx5(t), cfg)
	if err != nil || info.Status != HostKeyTrusted {
		t.Errorf("info=%+v err=%v", info, err)
	}
}

func TestWhenTheServerNoLongerOffersTheKnownKeyTypeTheErrorSaysSo(t *testing.T) {
	s := startSSHD(t) // offers only ed25519
	cfg := s.config(t)
	writeKnownHosts(t, cfg, newECDSASigner(t).PublicKey()) // known_hosts only has an ECDSA key

	_, err := InspectHostKey(ctx5(t), cfg)
	if err == nil || !strings.Contains(err.Error(), "no longer offers that key type") || !strings.Contains(err.Error(), "ecdsa") {
		t.Errorf("inspect err=%v, want an explanation that names the key type", err)
	}
	h, _ := Shared(cfg)
	t.Cleanup(h.Close)
	_, _, _, err = h.Run(ctx5(t), "x")
	if err == nil || !strings.Contains(err.Error(), "no longer offers that key type") {
		t.Errorf("connect err=%v, want the same explanation", err)
	}
	if s.authRequests.Load() != 0 {
		t.Errorf("authentication started (%d requests)", s.authRequests.Load())
	}
}

func TestHostKeyAlgorithmPreference(t *testing.T) {
	got := preferredHostKeyAlgos()
	if got[0] != gossh.KeyAlgoED25519 {
		t.Errorf("first = %q, want ed25519", got[0])
	}
	if got[len(got)-1] != gossh.KeyAlgoRSA {
		t.Errorf("last = %q, want the SHA-1 ssh-rsa kept only as a last resort for old servers", got[len(got)-1])
	}
	seen := map[string]bool{}
	for _, a := range got {
		if seen[a] {
			t.Errorf("%q is listed twice", a)
		}
		seen[a] = true
	}
	for _, want := range []string{gossh.KeyAlgoECDSA256, gossh.KeyAlgoRSASHA256, gossh.KeyAlgoRSASHA512} {
		if !seen[want] {
			t.Errorf("%q is missing from the preference list", want)
		}
	}
}

func TestAlgorithmsForKnownKeys(t *testing.T) {
	ed, _ := newSigner(t)
	rsaKey := newRSASigner(t).PublicKey()
	ec := newECDSASigner(t).PublicKey()
	kk := func(k gossh.PublicKey) knownhosts.KnownKey { return knownhosts.KnownKey{Key: k} }

	if got := algosForKeys([]knownhosts.KnownKey{kk(ed.PublicKey())}); len(got) != 1 || got[0] != "ssh-ed25519" {
		t.Errorf("ed25519 -> %v", got)
	}
	if got := algosForKeys([]knownhosts.KnownKey{kk(rsaKey)}); strings.Join(got, ",") != "rsa-sha2-512,rsa-sha2-256,ssh-rsa" {
		t.Errorf("an RSA key can sign with three algorithms, got %v", got)
	}
	got := algosForKeys([]knownhosts.KnownKey{kk(ec), kk(ed.PublicKey()), kk(ec)})
	if len(got) != 2 {
		t.Errorf("duplicates must be dropped: %v", got)
	}
	if hasType([]knownhosts.KnownKey{kk(ec)}, ed.PublicKey()) || !hasType([]knownhosts.KnownKey{kk(ec), kk(rsaKey)}, rsaKey) {
		t.Error("hasType compares key types")
	}
}

// Preferring ed25519 means the common case (what a normal ssh login stores)
// connects at the first attempt. Another known type costs exactly one retry.
func TestHowManyHandshakesAConnectionNeeds(t *testing.T) {
	for name, tc := range map[string]struct {
		pick  func(ed, ec, rs gossh.Signer) gossh.Signer
		conns int32
	}{
		"ed25519 is known: first attempt": {func(ed, ec, rs gossh.Signer) gossh.Signer { return ed }, 1},
		"ecdsa is known: one retry":       {func(ed, ec, rs gossh.Signer) gossh.Signer { return ec }, 2},
		"rsa is known: one retry":         {func(ed, ec, rs gossh.Signer) gossh.Signer { return rs }, 2},
	} {
		t.Run(name, func(t *testing.T) {
			s := startSSHD(t)
			cfg := s.config(t)
			writeKnownHosts(t, cfg, tc.pick(s.offerSeveralKeys(t)).PublicKey())
			h, _ := Shared(cfg)
			t.Cleanup(h.Close)
			if _, _, _, err := h.Run(ctx5(t), "x"); err != nil {
				t.Fatal(err)
			}
			if got := s.conns.Load(); got != tc.conns {
				t.Errorf("%d TCP connections, want %d", got, tc.conns)
			}
		})
	}
}
