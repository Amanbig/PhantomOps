// Package kubeconfig decides which kubeconfig file kubectl should use.
//
// Apps started from a desktop menu do not read the user's shell startup files,
// so the KUBECONFIG exported in ~/.bashrc is missing. On k3s machines
// `kubectl` is a symlink to the k3s binary, which then falls back to
// /etc/rancher/k3s/k3s.yaml (readable by root only) instead of ~/.kube/config.
// Resolve makes PhantomOps behave the same as it does when started from a
// terminal.
package kubeconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Source says where the chosen kubeconfig came from.
type Source string

const (
	SourceConnection Source = "connection" // set on the PhantomOps connection
	SourceEnv        Source = "env"        // $KUBECONFIG
	SourceHome       Source = "home"       // ~/.kube/config
	SourceDefault    Source = "default"    // left to kubectl
)

// expand turns "~" and "~/x" into the home directory.
func expand(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Resolve returns the kubeconfig kubectl should use, in this order: the
// connection's explicit path, $KUBECONFIG, ~/.kube/config (if it exists).
// An empty path means "let kubectl decide".
func Resolve(explicit string) (string, Source) {
	if p := expand(explicit); p != "" {
		return p, SourceConnection
	}
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return p, SourceEnv
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".kube", "config")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, SourceHome
		}
	}
	return "", SourceDefault
}

// Check verifies an explicitly configured kubeconfig file before kubectl is
// run: kubectl silently ignores a missing file and then tries
// http://localhost:8080, which produces a misleading error.
func Check(explicit string) error {
	p := expand(explicit)
	if p == "" {
		return nil
	}
	st, err := os.Stat(p)
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("the kubeconfig file %s does not exist", p)
	case os.IsPermission(err):
		return fmt.Errorf("the kubeconfig file %s cannot be read (permission denied)", p)
	case err != nil:
		return fmt.Errorf("the kubeconfig file %s cannot be used: %w", p, err)
	case st.IsDir():
		return fmt.Errorf("%s is a folder; choose the kubeconfig file itself", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("the kubeconfig file %s cannot be read: %w", p, err)
	}
	return f.Close()
}

// Command builds a kubectl command that uses the resolved kubeconfig.
func Command(explicit string, build func() *exec.Cmd) *exec.Cmd {
	cmd := build()
	path, src := Resolve(explicit)
	if path == "" || src == SourceEnv {
		return cmd // kubectl already sees the same KUBECONFIG
	}
	cmd.Env = append(os.Environ(), "KUBECONFIG="+path)
	return cmd
}

// Explain turns kubectl's stderr into a message a person can act on. If the
// text is not a known failure it is returned unchanged.
func Explain(stderr string) string {
	s := strings.TrimSpace(stderr)
	switch {
	case strings.Contains(s, "permission denied") && strings.Contains(s, "k3s.yaml"):
		return "kubectl tried /etc/rancher/k3s/k3s.yaml, which only root can read, and found no kubeconfig of yours. " +
			"Create one with:  mkdir -p ~/.kube && sudo k3s kubectl config view --raw > ~/.kube/config && chmod 600 ~/.kube/config  " +
			"(or set the kubeconfig file in this connection)."
	case strings.Contains(s, "permission denied") && strings.Contains(s, "config"):
		return "kubectl cannot read its kubeconfig (permission denied). Fix the file's permissions or set another kubeconfig file in this connection. Details: " + firstLine(s)
	case strings.Contains(s, "localhost:8080"):
		return "kubectl found no cluster to talk to (it fell back to localhost:8080). Set the kubeconfig file in this connection, or create ~/.kube/config."
	case strings.Contains(s, "no configuration has been provided"), strings.Contains(s, "Missing or incomplete configuration info"):
		return "kubectl has no kubeconfig. Set the kubeconfig file in this connection, or create ~/.kube/config."
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
