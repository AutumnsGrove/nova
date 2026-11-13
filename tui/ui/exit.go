//go:build !notui

package ui

import (
	"fmt"

	"github.com/AutumnsGrove/nova/tui/sanitize"
)

// resumeHint is the line the TUI leaves in the terminal after it stops, so a
// conversation can be picked up again without hunting through the picker. It is
// empty when there is nothing to return to: a session that received no entry is
// removed on close, and --no-session writes no file at all.
//
// The id comes from a session file, which is untrusted data, so it is passed
// through sanitize before it reaches a terminal.
func resumeHint(id string, entries int) string {
	if id == "" || entries == 0 {
		return ""
	}
	return fmt.Sprintf("\nTo resume this session: nova --session %s\n", sanitize.Text(id))
}
