//go:build !notui

package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// pathModel returns a model whose working directory holds one file, "alpha.txt".
func pathModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _, _ := newModel(t)
	m.cwd = dir
	return m
}

func TestUI_CPL09_StaleDirectoryReadIsDropped(t *testing.T) {
	m := pathModel(t)
	m.setEditorText("@a")
	read := m.loadPaths("@a", 0) // the directory read is in flight
	m.setEditorText("")          // the user clears the prompt before it lands
	update(m, do(read))
	if m.completion.active {
		t.Fatalf("a late directory read reopened the popup over an empty prompt: %+v", m.completion)
	}
}

func TestUI_CPL10_ReadForAnEarlierTokenIsDropped(t *testing.T) {
	m := pathModel(t)
	m.setEditorText("@a")
	read := m.loadPaths("@a", 0)
	m.setEditorText("@b") // the token changed; its own read is still to come
	update(m, do(read))
	if m.completion.active {
		t.Fatalf("results for @a were shown for @b: %+v", m.completion)
	}
}

func TestUI_CPL11_CurrentDirectoryReadOpensThePopup(t *testing.T) {
	m := pathModel(t)
	m.setEditorText("@a")
	update(m, do(m.loadPaths("@a", 0)))
	if !m.completion.active || len(m.completion.items) != 1 || m.completion.items[0].label != "alpha.txt" {
		t.Fatalf("completion = %+v, want alpha.txt", m.completion)
	}
}
