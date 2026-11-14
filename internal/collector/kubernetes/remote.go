package kubernetes

import (
	"context"
	"fmt"
	"strings"

	"PhantomOps/internal/collector/ssh"
	"PhantomOps/internal/kubeconfig"
)

// remoteRunner is implemented by the verified SSH command runner.
type remoteRunner interface {
	Run(ctx context.Context, command string) (stdout, stderr string, code int, err error)
}

// remoteAllowed is the complete set of kubectl calls that may run on a remote
// host: exactly the read-only calls the collector and the dashboard make.
// Anything else (delete, exec, apply, logs, secrets...) is refused.
var remoteAllowed = func() map[string]bool {
	m := map[string]bool{}
	for _, args := range [][]string{
		{"get", "pods", "-A", "-o", "json"},
		{"get", "events", "-A", "-o", "json"},
		{"get", "events", "-A", "--field-selector", "type=Warning", "-o", "json"},
		{"get", "deployments", "-A", "-o", "json"},
		{"get", "nodes", "-o", "json"},
		{"get", "--raw", "/apis/metrics.k8s.io/v1beta1/pods"},
		{"get", "--raw", "/apis/metrics.k8s.io/v1beta1/nodes"},
		{"version", "-o", "json"},
	} {
		m[strings.Join(args, "\x00")] = true
	}
	return m
}()

// NewRemote creates a read-only Kubernetes collector that runs kubectl on the
// host reached through the supplied, host-key-verified SSH config.
// remoteKubeconfig is an absolute path on that host; empty uses kubectl's own
// lookup there ($KUBECONFIG, then ~/.kube/config).
func NewRemote(sshCfg ssh.Config, kubeContext, remoteKubeconfig string) (*Collector, error) {
	r, err := ssh.Shared(sshCfg)
	if err != nil {
		return nil, err
	}
	return NewWithRunner(remoteRunnerFunc(r, kubeContext, remoteKubeconfig)), nil
}

func remoteRunnerFunc(r remoteRunner, kubeContext, remoteKubeconfig string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if !remoteAllowed[strings.Join(args, "\x00")] {
			return nil, fmt.Errorf("refusing to run kubectl %s on a remote host: not on the read-only allowlist", strings.Join(args, " "))
		}
		full := []string{"kubectl"}
		if kubeContext != "" {
			full = append(full, "--context", kubeContext)
		}
		full = append(full, args...)
		cmd := shellJoin(full)
		if remoteKubeconfig != "" {
			cmd = "KUBECONFIG=" + shellQuote(remoteKubeconfig) + " " + cmd
		}
		out, errOut, code, err := r.Run(ctx, cmd)
		if err != nil {
			return nil, fmt.Errorf("remote kubectl %s: %w", strings.Join(args, " "), err)
		}
		if code != 0 {
			return nil, fmt.Errorf("remote kubectl %s exited %d: %s", strings.Join(args, " "), code, explainRemote(code, errOut, out))
		}
		return []byte(out), nil
	}
}

// explainRemote turns common remote failures into advice.
func explainRemote(code int, stderr, stdout string) string {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = strings.TrimSpace(stdout)
	}
	l := strings.ToLower(msg)
	if code == 127 || strings.Contains(l, "kubectl: command not found") || strings.Contains(l, "kubectl: not found") {
		return "kubectl was not found on the remote host (is it installed and on the SSH user's PATH? Non-interactive SSH sessions do not read ~/.bashrc)"
	}
	msg = kubeconfig.Explain(msg)
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return msg
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
