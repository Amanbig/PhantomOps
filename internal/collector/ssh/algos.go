package ssh

import (
	"errors"
	"fmt"
	"net"
	"strings"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// A server usually has several host keys (ed25519, ECDSA, RSA). The client and
// server agree on one during the handshake, and Go's default order prefers
// ECDSA over ed25519. Without help that causes a false alarm: a known_hosts
// file that holds only the ed25519 key (which is what the normal ssh command
// stores) looks like a "changed key" because the server showed its ECDSA key.
//
// PhantomOps therefore behaves like OpenSSH:
//   - for a server it does not know yet, it prefers the ed25519 key;
//   - for a server it knows, it asks for the key types known_hosts already has
//     for that server. A key of the same type that differs is a real change;
//     a different key type is not.

// preferredHostKeyAlgos is the order offered for a server that is not in
// known_hosts: ed25519 first, then the library's secure algorithms, then
// ssh-rsa (SHA-1) last so very old servers keep working.
func preferredHostKeyAlgos() []string {
	out := []string{gossh.KeyAlgoED25519}
	for _, a := range gossh.SupportedAlgorithms().HostKeys {
		if a != gossh.KeyAlgoED25519 {
			out = append(out, a)
		}
	}
	return append(out, gossh.KeyAlgoRSA)
}

// algosForKeys returns the signature algorithms that can prove the given
// known host keys.
func algosForKeys(keys []knownhosts.KnownKey) []string {
	seen := map[string]bool{}
	var out []string
	add := func(a string) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for _, k := range keys {
		switch k.Key.Type() {
		case gossh.KeyAlgoRSA: // an RSA key can sign with three algorithms
			add(gossh.KeyAlgoRSASHA512)
			add(gossh.KeyAlgoRSASHA256)
			add(gossh.KeyAlgoRSA)
		default:
			add(k.Key.Type())
		}
	}
	return out
}

// hasType reports whether keys contains a key of the same type as offered.
func hasType(keys []knownhosts.KnownKey, offered gossh.PublicKey) bool {
	for _, k := range keys {
		if k.Key.Type() == offered.Type() {
			return true
		}
	}
	return false
}

// typeNames lists the key types in keys for messages, like "ssh-ed25519".
func typeNames(keys []knownhosts.KnownKey) string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		if t := k.Key.Type(); !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return strings.Join(out, ", ")
}

// hostVerifier checks a server's key against known_hosts and remembers what it
// saw, so the caller can tell "the server chose another key type" (retry
// asking for a known type) from "the key of a known type changed" (refuse).
type hostVerifier struct {
	check   gossh.HostKeyCallback
	addr    string
	offered gossh.PublicKey
	want    []knownhosts.KnownKey
}

func (v *hostVerifier) callback(hostname string, remote net.Addr, key gossh.PublicKey) error {
	v.offered, v.want = key, nil
	err := v.check(hostname, remote, key)
	if err == nil {
		return nil
	}
	var ke *knownhosts.KeyError
	if errors.As(err, &ke) {
		v.want = ke.Want
	}
	return describeKeyError(v.addr, key, err)
}

// retryAlgos returns the algorithms to ask for on a second attempt, or nil if
// a retry would not help. It is non-nil only when known_hosts has keys for
// this server but none of the type the server just showed.
func (v *hostVerifier) retryAlgos() []string {
	if len(v.want) == 0 || v.offered == nil || hasType(v.want, v.offered) {
		return nil
	}
	return algosForKeys(v.want)
}

// explainNoCommonKey turns the library's "no common algorithm for host key"
// failure on a retry into advice.
func (v *hostVerifier) explainNoCommonKey(err error) error {
	if err == nil || !strings.Contains(err.Error(), "no common algorithm for host key") {
		return err
	}
	return fmt.Errorf("known_hosts has only a %s key for %s, but the server no longer offers that key type. "+
		"This can be a server reconfiguration or an interception attempt. Verify the server's current key with its owner, then update known_hosts yourself: %w",
		typeNames(v.want), v.addr, err)
}
