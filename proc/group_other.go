//go:build windows

// Package proc holds the process-group primitives that the bash tool and the
// extension host share. Windows has no process groups to signal, so these do
// nothing there.
package proc

import (
	"errors"
	"os/exec"
)

// SetGroup has no process groups to set on Windows.
func SetGroup(cmd *exec.Cmd) {}

// Terminate has no process group to signal on Windows.
func Terminate(pid int) {}

// Kill has no process group to signal on Windows.
func Kill(pid int) {}

// ExitStatus extracts the exit code from a Wait error; Windows reports no
// signal.
func ExitStatus(err error) (code, signal int) {
	if err == nil {
		return 0, 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), 0
	}
	return -1, 0
}
