//go:build windows

package session

import "os"

// lockFile is a no-op where flock does not exist.
func lockFile(f *os.File) error { return nil }

// unlockFile is a no-op where flock does not exist.
func unlockFile(f *os.File) error { return nil }
