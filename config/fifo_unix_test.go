//go:build unix

package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// within fails the test when fn does not return before the deadline. A FIFO read
// blocks forever, so the check has to run off the test goroutine.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return: it is blocked reading a non-regular file", what)
	}
}

// mkfifo creates a named pipe at path with no writer, which blocks any reader.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
}

// TestContext_CTF15_NamedPipeSkipped checks a FIFO named AGENTS.md is skipped
// with a warning instead of hanging startup on a read that never returns.
func TestContext_CTF15_NamedPipeSkipped(t *testing.T) {
	f := newCtxFixture(t)
	mkfifo(t, filepath.Join(f.b, "AGENTS.md"))
	var files []ContextFile
	var warnings []Warning
	within(t, 2*time.Second, "LoadContext", func() { files, warnings = f.load(t) })
	if len(files) != 0 {
		t.Fatalf("files = %v, want none", pathsOf(files))
	}
	if len(warnings) == 0 {
		t.Fatal("no warning for the skipped pipe")
	}
}

// TestLoad_CFG33_NamedPipeConfigDoesNotBlock checks that a FIFO in place of a
// config file, in the user directory or in a project that is not trusted,
// yields a warning rather than a hang. Opening an untrusted repo must never
// stall on a file the repo planted.
func TestLoad_CFG33_NamedPipeConfigDoesNotBlock(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	mkfifo(t, filepath.Join(home, ".config", "nova", "config.yaml"))
	mkfifo(t, filepath.Join(cwd, ".nova", "config.yaml"))
	var warnings []Warning
	within(t, 2*time.Second, "Load", func() { _, warnings = loadAt(t, cwd, home, LoadOptions{}) })
	if len(warnings) == 0 {
		t.Fatal("no warning for the unreadable user config")
	}
}

// TestSkills_SKL14_NamedPipeSkipped is CTF15 for SKILL.md.
func TestSkills_SKL14_NamedPipeSkipped(t *testing.T) {
	root := t.TempDir()
	mkfifo(t, filepath.Join(root, "pipe", "SKILL.md"))
	within(t, 2*time.Second, "LoadSkills", func() { LoadSkills([]string{root}) })
}

// TestTemplates_TPL16_NamedPipeSkipped is CTF15 for a prompt template.
func TestTemplates_TPL16_NamedPipeSkipped(t *testing.T) {
	root := t.TempDir()
	mkfifo(t, filepath.Join(root, "pipe.md"))
	var templates []Template
	within(t, 2*time.Second, "LoadTemplates", func() { templates, _ = LoadTemplates([]string{root}) })
	if len(templates) != 0 {
		t.Fatalf("templates = %+v, want none", templates)
	}
}
