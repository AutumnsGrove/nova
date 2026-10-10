package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// TestResolve_PTH01_Relative checks a relative path resolves against WorkDir.
func TestResolve_PTH01_Relative(t *testing.T) {
	dir := t.TempDir()
	got, err := resolve(&tool.Context{WorkDir: dir}, "a.txt")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join(dir, "a.txt"); got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

// TestResolve_PTH02_Absolute checks an absolute path is cleaned and kept.
func TestResolve_PTH02_Absolute(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "y")
	got, err := resolve(nil, filepath.Join(dir, "x", "..", "y"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

// TestResolve_PTH03_Empty checks an empty path is refused.
func TestResolve_PTH03_Empty(t *testing.T) {
	if _, err := resolve(nil, ""); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("resolve error = %v, want path is required", err)
	}
}

// TestResolve_PTH04_NUL checks a NUL byte is refused.
func TestResolve_PTH04_NUL(t *testing.T) {
	if _, err := resolve(nil, "a\x00b"); err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Fatalf("resolve error = %v, want invalid path", err)
	}
}

// TestResolve_PTH05_TooLong checks a path over the cap is refused.
func TestResolve_PTH05_TooLong(t *testing.T) {
	long := strings.Repeat("a", maxPathBytes+1)
	if _, err := resolve(nil, long); err == nil || !strings.Contains(err.Error(), "path too long") {
		t.Fatalf("resolve error = %v, want path too long", err)
	}
}

// TestResolve_PTH06_Cleans checks .. segments are cleaned.
func TestResolve_PTH06_Cleans(t *testing.T) {
	dir := t.TempDir()
	got, err := resolve(&tool.Context{WorkDir: dir}, "a/../b")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join(dir, "b"); got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

// TestResolve_PTH07_InsideRoot checks a path inside a root is allowed.
func TestResolve_PTH07_InsideRoot(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolve(&tool.Context{WorkDir: dir, Roots: []string{dir}}, "a.txt"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

// TestResolve_PTH08_OutsideRoot checks a path that escapes a root is refused.
func TestResolve_PTH08_OutsideRoot(t *testing.T) {
	dir := t.TempDir()
	ctx := &tool.Context{WorkDir: dir, Roots: []string{dir}}
	if _, err := resolve(ctx, "../x"); err == nil || !strings.Contains(err.Error(), "outside the allowed directories") {
		t.Fatalf("resolve error = %v, want outside the allowed directories", err)
	}
}

// TestResolve_PTH09_SymlinkOut checks a symlink inside a root that points
// outside is refused.
func TestResolve_PTH09_SymlinkOut(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	secret := testkit.TempTree(t, map[string]string{"secret.txt": "x"})
	testkit.Symlink(t, root, "link", secret)
	ctx := &tool.Context{WorkDir: root, Roots: []string{root}}
	if _, err := resolve(ctx, "link"); err == nil || !strings.Contains(err.Error(), "outside the allowed directories") {
		t.Fatalf("resolve error = %v, want outside the allowed directories", err)
	}
}

// TestResolve_PTH10_NewFileInRoot checks a new file in a missing subdirectory
// of a root is allowed.
func TestResolve_PTH10_NewFileInRoot(t *testing.T) {
	root := t.TempDir()
	ctx := &tool.Context{WorkDir: root, Roots: []string{root}}
	if _, err := resolve(ctx, "a/b/c.txt"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

// TestResolve_PTH11_SymlinkedParent checks a new file under a symlinked parent
// that leaves the root is refused.
func TestResolve_PTH11_SymlinkedParent(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	testkit.Symlink(t, root, "link", outside)
	ctx := &tool.Context{WorkDir: root, Roots: []string{root}}
	if _, err := resolve(ctx, "link/new.txt"); err == nil || !strings.Contains(err.Error(), "outside the allowed directories") {
		t.Fatalf("resolve error = %v, want outside the allowed directories", err)
	}
}

// TestResolve_PTH12_EmptyWorkDir checks an empty WorkDir uses the process
// directory.
func TestResolve_PTH12_EmptyWorkDir(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	got, err := resolve(&tool.Context{}, "a.txt")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join(wd, "a.txt"); got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

// TestResolve_PTH13_Unicode checks spaces and Unicode in a path.
func TestResolve_PTH13_Unicode(t *testing.T) {
	dir := t.TempDir()
	got, err := resolve(&tool.Context{WorkDir: dir}, "dir é/ファイル.txt")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasPrefix(got, dir) {
		t.Fatalf("resolve = %q, want a path under %q", got, dir)
	}
}

// FuzzResolve checks resolve never panics and returns an absolute clean path or
// an error.
func FuzzResolve(f *testing.F) {
	f.Add("a/b.txt")
	f.Add("../x")
	f.Add("a\x00b")
	f.Add("")
	f.Fuzz(func(t *testing.T, path string) {
		got, err := resolve(&tool.Context{WorkDir: t.TempDir()}, path)
		if err != nil {
			return
		}
		if !filepath.IsAbs(got) || got != filepath.Clean(got) {
			t.Fatalf("resolve(%q) = %q, want an absolute clean path", path, got)
		}
	})
}
