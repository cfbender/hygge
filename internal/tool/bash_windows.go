//go:build windows

package tool

import (
	"os/exec"
	"time"
)

// configureProcessGroup is a best-effort stub on Windows. Portable Windows
// shell-out (process groups / job objects for full descendant teardown) is a
// known follow-up; for now we at least bound how long Wait blocks on pipes held
// open by a killed command so an interrupt can never hang the tool.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = 5 * time.Second
}
