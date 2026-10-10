//go:build !windows

package session

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on f. It returns an error only for
// a real failure; a filesystem without flock support is reported.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// unlockFile releases the advisory lock.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
