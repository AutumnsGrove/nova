//go:build !windows

package ext

import (
	"errors"
	"os/exec"
	"syscall"
)

// setProcessGroup puts cmd in its own process group, so the whole tree can be
// signalled at once and a forked child cannot outlive the extension.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateGroup asks every process in the group to stop.
func terminateGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGTERM) }

// forceKillGroup kills every process in the group.
func forceKillGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }

// exitCode extracts a command's exit code, or -1 when it died from a signal.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return exitErr.ExitCode()
	}
	return -1
}
