// Package gitexec provides a non-interactive git execution seam that can be
// replaced with fakes in tests.
package gitexec

import (
	"context"
	"os"
	"os/exec"
)

// Runner executes git with a non-interactive, credential-helper-neutralized
// environment. Implementations are safe to fake in tests so callers never need
// to invoke a real git binary.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// DefaultRunner is the production git runner.
type DefaultRunner struct{}

// Run executes git with args in dir. Empty dir inherits the caller's working
// directory. Output includes stdout and stderr so failures carry useful detail.
func (DefaultRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	full := append([]string{
		"-c", "credential.helper=",
		"-c", "core.askPass=",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // args are assembled by callers from validated inputs
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/echo",
		"SSH_ASKPASS=/bin/echo",
		"GCM_INTERACTIVE=Never",
	)
	cmd.Stdin = nil
	return cmd.CombinedOutput()
}
