//go:build !notui

package ui

import (
	"strings"
	"testing"
)

// The hint is the one line the TUI leaves in the terminal after it stops, so a
// conversation can be picked up again without hunting through the picker.
func TestUI_RSM03_ResumeHint(t *testing.T) {
	got := resumeHint("cf40938bb280e649", 4)
	if !strings.Contains(got, "nova --session cf40938bb280e649") {
		t.Fatalf("hint = %q, want the resume command", got)
	}
}

// Nothing to resume means nothing to print: an empty session is removed when
// the UI closes, and a session that is not written has no id at all.
func TestUI_RSM04_ResumeHintEmpty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		id      string
		entries int
	}{
		{"empty session", "cf40938bb280e649", 0},
		{"no session", "", 3},
	} {
		if got := resumeHint(tc.id, tc.entries); got != "" {
			t.Fatalf("%s: hint = %q, want none", tc.name, got)
		}
	}
}

// The id comes from a file on disk, which is untrusted data: a terminal escape
// in it must not reach the terminal.
func TestUI_RSM05_ResumeHintSanitized(t *testing.T) {
	got := resumeHint("bad\x1b[2Jid", 1)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("hint = %q, want the escape stripped", got)
	}
}
