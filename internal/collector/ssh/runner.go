package ssh

import "context"

// CommandRunner opens a verified SSH session for each command. It is used by
// read-only integrations that need to inspect a service on the same host.
// Callers must pass only fixed, read-only commands; user input must never be
// concatenated into a command string.
type CommandRunner struct {
	dial func(context.Context) (Exec, func(), error)
}

// NewCommandRunner creates a runner using the same authentication and host
// key verification as the SSH collector.
func NewCommandRunner(cfg Config) (*CommandRunner, error) {
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &CommandRunner{dial: c.dial}, nil
}

// Run executes one already-allowlisted read-only command.
func (r *CommandRunner) Run(ctx context.Context, command string) (string, int, error) {
	execFn, closeFn, err := r.dial(ctx)
	if err != nil {
		return "", 0, err
	}
	defer closeFn()
	return execFn(ctx, command)
}
