//go:build !notui

package ui

import (
	"errors"
	"os"
	"slices"
	"testing"
)

// prepared runs ctrl+g's first command and returns the editor start message.
func prepared(t *testing.T, m *Model) editorStartMsg {
	t.Helper()
	msg, ok := do(m.openEditor()).(editorStartMsg)
	if !ok {
		t.Fatal("openEditor did not yield an editorStartMsg")
	}
	if msg.path != "" {
		t.Cleanup(func() { _ = os.Remove(msg.path) })
	}
	return msg
}

func TestUI_EDI21_EditorWithArguments(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("draft")
	t.Setenv("EDITOR", "my-editor --wait --new-window")
	msg := prepared(t, m)
	if msg.err != nil {
		t.Fatalf("err = %v", msg.err)
	}
	want := []string{"my-editor", "--wait", "--new-window", msg.path}
	if !slices.Equal(msg.cmd.Args, want) {
		t.Fatalf("argv = %q, want %q", msg.cmd.Args, want)
	}
	if data, err := os.ReadFile(msg.path); err != nil || string(data) != "draft" {
		t.Fatalf("temp file = %q, %v; want the editor text", data, err)
	}
}

func TestUI_EDI22_BlankEditorVariable(t *testing.T) {
	m, _, _ := newModel(t)
	t.Setenv("EDITOR", "  \t ")
	if msg := prepared(t, m); !errors.Is(msg.err, errNoEditor) {
		t.Fatalf("err = %v, want errNoEditor", msg.err)
	}
}

func TestUI_EDI23_FailedEditorRemovesItsTempFile(t *testing.T) {
	m, _, _ := newModel(t)
	t.Setenv("EDITOR", "my-editor")
	msg := prepared(t, m)
	cmd := m.readEditor(editorFinishedMsg{path: msg.path, err: errors.New("exit status 1")})
	if m.notice == "" {
		t.Fatal("a failed editor set no notice")
	}
	do(cmd)
	if _, err := os.Stat(msg.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file still present after a failed editor: %v", err)
	}
}

func TestUI_EDI24_FinishedEditorReturnsTextAndCleansUp(t *testing.T) {
	m, _, _ := newModel(t)
	t.Setenv("EDITOR", "my-editor")
	msg := prepared(t, m)
	if err := os.WriteFile(msg.path, []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	update(m, do(m.readEditor(editorFinishedMsg{path: msg.path})))
	if got := m.editor.Value(); got != "edited" {
		t.Fatalf("editor = %q, want the file content without its trailing newline", got)
	}
	if _, err := os.Stat(msg.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file still present: %v", err)
	}
}
