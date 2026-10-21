//go:build !windows

// Package proc holds the process-group primitives that the bash tool and the
// extension host share: start a command in its own group, signal the whole
// group, and read how a command ended. It is a leaf; it imports nothing in the
// module.
package proc

import (
	"errors"
	"os/exec"
	"syscall"
)

// SetGroup puts cmd in its own process group, so the whole tree can be
// signalled at once and a backgrounded child cannot outlive the call.
func SetGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Terminate asks every process in the group led by pid to stop.
func Terminate(pid int) { _ = syscall.Kill(-pid, syscall.SIGTERM) }

// Kill kills every process in the group led by pid.
func Kill(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }

// ExitStatus extracts the exit code or the fatal signal from a Wait error. A
// signal death reports code -1 and the signal number; a failure that is not an
// exit (the command never started) reports -1 and no signal.
func ExitStatus(err error) (code, signal int) {
	if err == nil {
		return 0, 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return -1, 0
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return -1, int(ws.Signal())
	}
	return exitErr.ExitCode(), 0
}
