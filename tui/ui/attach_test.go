//go:build !notui

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUI_PST06_UpdateDoesNotReadAttachments(t *testing.T) {
	m, f, _ := newModel(t)
	writePNG(t, m.cwd, "shot.png")
	m.setEditorText("describe shot.png")
	cmd := update(m, enterKey)
	// Remove the file after Update returned: an attachment that still arrives
	// was read by Update itself, which must do no file I/O.
	if err := os.Remove(filepath.Join(m.cwd, "shot.png")); err != nil {
		t.Fatal(err)
	}
	execAll(cmd)
	if len(f.sends) != 1 || len(f.sends[0].Attachments) != 0 {
		t.Fatalf("sends = %+v; the file was read before the command ran", f.sends)
	}
}

func TestUI_PST07_RapidPromptsKeepTheirOwnText(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("first prompt")
	one := update(m, enterKey)
	m.setEditorText("second prompt")
	two := update(m, enterKey)
	// Both are in flight before either response is applied.
	msgOne, msgTwo := do(one), do(two)
	update(m, msgOne)
	update(m, msgTwo)
	v := m.render()
	if !strings.Contains(v, "first prompt") || !strings.Contains(v, "second prompt") {
		t.Fatalf("a prompt lost its text when two were in flight:\n%s", v)
	}
}

// A file name is whatever the file system holds, and a downloaded archive can
// make one that carries escape sequences.
func TestUI_PST08_AttachmentChipHasNoControlBytes(t *testing.T) {
	m, _, _ := newModel(t)
	m.pendingAtts = []pendingAttachment{{Name: "shot\x1b]0;pwned\x07\x1b[2J.png"}}
	chip := m.chipLine()
	if strings.Contains(chip, "\x1b]") || strings.Contains(chip, "[2J") || strings.ContainsRune(chip, '\x07') {
		t.Fatalf("chip carries control bytes: %q", chip)
	}
	if !strings.Contains(chip, "shot.png") {
		t.Fatalf("chip lost the visible name: %q", chip)
	}
}
