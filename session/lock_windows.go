//go:build windows

package session

import "os"

// lockFile is a no-op on Windows, where syscall has no flock: two processes
// appending to one session there are not serialised.
func lockFile(*os.File) error { return nil }

// unlockFile is a no-op on Windows; see lockFile.
func unlockFile(*os.File) error { return nil }
