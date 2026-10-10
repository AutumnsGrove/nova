//go:build windows

package bash

import (
	"errors"
	"fmt"
	"os/exec"
)

// supported reports that the bash tool is unavailable on Windows.
func supported() bool { return false }

// setProcessGroup has no process groups to set on Windows.
func setProcessGroup(cmd *exec.Cmd) {}

// terminateGroup has no process group to signal on Windows.
func terminateGroup(pid int) {}

// forceKillGroup has no process group to signal on Windows.
func forceKillGroup(pid int) {}

// exitStatus extracts the exit code from a Wait error.
func exitStatus(err error) (code, signal int) {
	if err == nil {
		return 0, 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), 0
	}
	return -1, 0
}

// signalName renders a signal number, which Windows never reports.
func signalName(n int) string { return fmt.Sprintf("SIG%d", n) }
