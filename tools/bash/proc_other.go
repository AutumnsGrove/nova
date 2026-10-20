//go:build windows

package bash

import "fmt"

// supported reports that the bash tool is unavailable on Windows.
func supported() bool { return false }

// signalName renders a signal number, which Windows never reports.
func signalName(n int) string { return fmt.Sprintf("SIG%d", n) }
