//go:build !windows

package tool

import (
	"os/exec"
	"syscall"
	"time"
)

// configureProcessGroup places the command and all of its descendants in a
// dedicated process group and arranges for the entire group to be killed when
// the command's context is cancelled or its timeout fires.
//
// Without this, exec.CommandContext only SIGKILLs the direct `sh` child. Any
// grandchildren that `sh` spawned (background jobs, dev servers, `make`
// subprocesses) are orphaned and keep running, accumulating CPU over the life
// of a session. Killing the negative PID targets the whole group.
//
// WaitDelay bounds how long Wait blocks on pipes that surviving descendants may
// still hold open after the group kill, so an interrupted command can never
// hang the tool indefinitely.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID signals the entire process group. SIGKILL is
		// unconditional: an interrupted/timed-out command should leave
		// nothing behind.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}
