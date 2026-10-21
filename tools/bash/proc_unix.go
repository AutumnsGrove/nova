//go:build !windows

package bash

import (
	"fmt"
	"syscall"
)

// supported reports whether the bash tool can run on this platform.
func supported() bool { return true }

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
