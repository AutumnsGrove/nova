//go:build !windows

package session

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on f, waiting for another holder.
// A signal can interrupt the wait (the Go runtime sends itself some), so
// EINTR retries instead of failing an append.
func lockFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// unlockFile releases the advisory lock.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
