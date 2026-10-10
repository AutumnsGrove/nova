//go:build windows

package ext

import (
	"errors"
	"os/exec"
)

// setProcessGroup has no process groups to set on Windows.
func setProcessGroup(cmd *exec.Cmd) {}

// terminateGroup has no process group to signal on Windows.
func terminateGroup(pid int) {}

// forceKillGroup has no process group to signal on Windows.
func forceKillGroup(pid int) {}

// exitCode extracts a command's exit code.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
