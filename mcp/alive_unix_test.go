//go:build unix

package mcp

import "syscall"

// alive reports whether a process still exists. Signal 0 asks the kernel without
// delivering anything.
func alive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}
