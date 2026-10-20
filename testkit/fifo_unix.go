//go:build unix

package testkit

import (
	"syscall"
	"testing"
)

// Mkfifo creates a named pipe at path, or skips the test where the platform or
// the filesystem has none. Tests use it for the file that blocks forever when a
// reader opens it with no writer.
func Mkfifo(t testing.TB, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
}
