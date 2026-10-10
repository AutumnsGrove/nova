//go:build !windows

package bash

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

// supported reports whether the bash tool can run on this platform.
func supported() bool { return true }

// setProcessGroup puts cmd in its own process group, so the whole tree can be
// signalled at once and a backgrounded child cannot outlive the call.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateGroup asks every process in the group to stop.
func terminateGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGTERM) }

// forceKillGroup kills every process in the group.
func forceKillGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }

// exitStatus extracts the exit code or the signal from a Wait error. A signal
// death reports code -1.
func exitStatus(err error) (code, signal int) {
	if err == nil {
		return 0, 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return -1, int(ws.Signal())
			}
			return ws.ExitStatus(), 0
		}
		return exitErr.ExitCode(), 0
	}
	return -1, 0
}

// signalNames maps the signals the tool names in a result.
var signalNames = map[syscall.Signal]string{
	syscall.SIGHUP:  "SIGHUP",
	syscall.SIGINT:  "SIGINT",
	syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGKILL: "SIGKILL",
	syscall.SIGPIPE: "SIGPIPE",
	syscall.SIGABRT: "SIGABRT",
	syscall.SIGSEGV: "SIGSEGV",
	syscall.SIGTERM: "SIGTERM",
}

// signalName renders a signal number as SIGNAME, or SIGn for an unknown one.
func signalName(n int) string {
	if name, ok := signalNames[syscall.Signal(n)]; ok {
		return name
	}
	return fmt.Sprintf("SIG%d", n)
}
