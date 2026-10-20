//go:build !unix

package testkit

import "testing"

// Mkfifo skips the test: this platform has no named pipes.
func Mkfifo(t testing.TB, path string) {
	t.Helper()
	t.Skip("no named pipes on this platform")
}
