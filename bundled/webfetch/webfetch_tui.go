//go:build !notui

package webfetch

import "fmt"

// Card is the TUI card summary for a web_fetch call.
func Card(url string, status, bytes int) string {
	return fmt.Sprintf("%s (%d, %d bytes)", url, status, bytes)
}
