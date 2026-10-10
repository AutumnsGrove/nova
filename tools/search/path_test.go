package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/tool"
)

func TestResolve_RelativeToWorkDir(t *testing.T) {
	dir := t.TempDir()
	got, err := resolve(&tool.Context{WorkDir: dir}, "sub/file.txt")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join(dir, "sub", "file.txt"); got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

func TestResolve_EmptyAndNUL(t *testing.T) {
	if _, err := resolve(nil, ""); err == nil {
		t.Fatal("empty path should error")
	}
	if _, err := resolve(nil, "a\x00b"); err == nil {
		t.Fatal("NUL path should error")
	}
	if _, err := resolve(nil, strings.Repeat("x", pathMaxBytes+1)); err == nil {
		t.Fatal("over-long path should error")
	}
}

func TestResolve_RootsContainment(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := resolve(&tool.Context{WorkDir: dir, Roots: []string{root}}, "root/inside.txt"); err != nil {
		t.Fatalf("inside root should resolve: %v", err)
	}
	if _, err := resolve(&tool.Context{WorkDir: dir, Roots: []string{root}}, "outside/x.txt"); err == nil {
		t.Fatal("outside root should error")
	}
}

func TestResolve_RootsFollowSymlinkedParent(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := resolve(&tool.Context{WorkDir: dir, Roots: []string{root}}, "root/link/new.txt"); err == nil {
		t.Fatal("a symlinked parent escaping the root should error")
	}
}
