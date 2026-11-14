//go:build !notui

package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/tui/sanitize"
)

// resumeHint is the command that brings the user back to this conversation,
// printed after the TUI stops. It is empty when there is nothing to return to.
//
// The short form names the session by id, which is what the picker and
// `nova sessions` show. That only resolves when the file sits in the session
// directory of the working directory, so a run started with --cwd or --session
// prints its quoted path instead, which works from anywhere.
func resumeHint(cwd, id, path string, resumable bool) string {
	if !resumable || path == "" {
		return ""
	}
	target := shellQuote(sanitize.Line(path))
	if filepath.Dir(path) == session.DefaultDir(cwd) && safeID(id) {
		target = id
	}
	return fmt.Sprintf("\nTo resume this session: nova --session %s\n", target)
}

// safeID reports whether id can be pasted into a shell as one word. The id is
// read from a session file, which is untrusted data, and the hint invites the
// user to run what it prints: anything outside this alphabet could be a second
// command. Nova generates e_ and eight hex digits; the wider set is there so a
// future id format does not silently lose the short form.
func safeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// shellQuote wraps s in single quotes, where a POSIX shell and PowerShell take
// everything literally. A single quote cannot appear inside, so it is closed,
// escaped, and reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
