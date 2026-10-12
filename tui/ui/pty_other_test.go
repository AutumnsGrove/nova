//go:build !notui && !linux && !darwin

package ui

import (
	"os"
	"testing"
)

// openPTY is unavailable on this platform.
func openPTY(t *testing.T) (*os.File, *os.File) {
	t.Skip("the PTY smoke tests need Linux or macOS")
	return nil, nil
}
