//go:build windows

package mcp

import "syscall"

// alive reports whether a process still exists. Windows keeps the handle open
// after exit, so the open question is whether it can be signalled.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(p)
	return true
}
