package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// writeKey writes an ed25519 private key to a temp file, encrypted when
// passphrase is not empty, and returns its path and the public key.
func writeKey(t *testing.T, passphrase string) (string, gossh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blk *pem.Block
	if passphrase == "" {
		blk, err = gossh.MarshalPrivateKey(priv, "")
	} else {
		blk, err = gossh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(path, pem.EncodeToMemory(blk), 0o600); err != nil {
		t.Fatal(err)
	}
	sp, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return path, sp
}

func TestEncryptedKeyUnlocksWithPassphrase(t *testing.T) {
	path, want := writeKey(t, "correct horse")
	methods, closeFn, err := authMethods(Config{KeyPath: path, Passphrase: "correct horse"})
	if err != nil {
		t.Fatalf("authMethods: %v", err)
	}
	defer closeFn()
	if len(methods) != 1 {
		t.Fatalf("want one auth method, got %d", len(methods))
	}
	pem, _ := os.ReadFile(path)
	signer, err := parseKey(pem, path, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if string(signer.PublicKey().Marshal()) != string(want.Marshal()) {
		t.Error("unlocked key is not the key that was written")
	}
}

func TestEncryptedKeyErrorsAreClearAndNeverLeakThePassphrase(t *testing.T) {
	path, _ := writeKey(t, "correct horse")

	_, _, err := authMethods(Config{KeyPath: path})
	if err == nil || !strings.Contains(err.Error(), "protected by a passphrase") || !strings.Contains(err.Error(), "ssh-agent") {
		t.Errorf("missing passphrase should explain both ways out, got: %v", err)
	}

	_, _, err = authMethods(Config{KeyPath: path, Passphrase: "wrong-secret-123"})
	if err == nil || !strings.Contains(err.Error(), "passphrase") || !strings.Contains(err.Error(), "wrong") {
		t.Errorf("wrong passphrase should say so, got: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "wrong-secret-123") {
		t.Errorf("error leaks the passphrase: %v", err)
	}
}

func TestUnencryptedKeyIgnoresAPassphrase(t *testing.T) {
	path, _ := writeKey(t, "")
	if _, closeFn, err := authMethods(Config{KeyPath: path, Passphrase: "unused"}); err != nil {
		t.Fatalf("a plain key must still work when a passphrase is set: %v", err)
	} else {
		closeFn()
	}
}

func TestPoolSeparatesConnectionsByPassphrase(t *testing.T) {
	a, err := Shared(Config{Addr: "pool.test:22", User: "u", KeyPath: "/k", Passphrase: "one"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Shared(Config{Addr: "pool.test:22", User: "u", KeyPath: "/k", Passphrase: "two"})
	c, _ := Shared(Config{Addr: "pool.test:22", User: "u", KeyPath: "/k", Passphrase: "one"})
	if a == b {
		t.Error("different passphrases must not share a pooled connection")
	}
	if a != c {
		t.Error("the same settings should share one pooled connection")
	}
}
