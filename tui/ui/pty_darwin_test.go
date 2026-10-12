//go:build !notui && darwin

package ui

import (
	"bytes"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY allocates a pseudo-terminal pair on macOS.
func openPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("open /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCPTYGRANT, 0); err != nil {
		master.Close()
		t.Skipf("grantpt: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCPTYUNLK, 0); err != nil {
		master.Close()
		t.Skipf("unlockpt: %v", err)
	}
	var buf [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		master.Close()
		t.Skipf("TIOCPTYGNAME: %v", errno)
	}
	name := string(buf[:bytes.IndexByte(buf[:], 0)])
	slave, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		master.Close()
		t.Skipf("open pty slave: %v", err)
	}
	_ = unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 80})
	return master, slave
}
