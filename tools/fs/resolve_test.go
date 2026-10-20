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
	got, err := tool.ResolvePath(&tool.Context{WorkDir: dir}, "a.txt")
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
	got, err := tool.ResolvePath(nil, filepath.Join(dir, "x", "..", "y"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

// TestResolve_PTH03_Empty checks an empty path is refused.
func TestResolve_PTH03_Empty(t *testing.T) {
	if _, err := tool.ResolvePath(nil, ""); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("resolve error = %v, want path is required", err)
	}
}

// TestResolve_PTH04_NUL checks a NUL byte is refused.
func TestResolve_PTH04_NUL(t *testing.T) {
	if _, err := tool.ResolvePath(nil, "a\x00b"); err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Fatalf("resolve error = %v, want invalid path", err)
	}
}

// TestResolve_PTH05_TooLong checks a path over the cap is refused.
func TestResolve_PTH05_TooLong(t *testing.T) {
	long := strings.Repeat("a", 4097)
	if _, err := tool.ResolvePath(nil, long); err == nil || !strings.Contains(err.Error(), "path too long") {
		t.Fatalf("resolve error = %v, want path too long", err)
	}
}

// TestResolve_PTH06_Cleans checks .. segments are cleaned.
func TestResolve_PTH06_Cleans(t *testing.T) {
	dir := t.TempDir()
	got, err := tool.ResolvePath(&tool.Context{WorkDir: dir}, "a/../b")
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
	if _, err := tool.ResolvePath(&tool.Context{WorkDir: dir, Roots: []string{dir}}, "a.txt"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

// TestResolve_PTH08_OutsideRoot checks a path that escapes a root is refused.
func TestResolve_PTH08_OutsideRoot(t *testing.T) {
	dir := t.TempDir()
	ctx := &tool.Context{WorkDir: dir, Roots: []string{dir}}
	if _, err := tool.ResolvePath(ctx, "../x"); err == nil || !strings.Contains(err.Error(), "outside the allowed directories") {
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
	if _, err := tool.ResolvePath(ctx, "link"); err == nil || !strings.Contains(err.Error(), "outside the allowed directories") {
		t.Fatalf("resolve error = %v, want outside the allowed directories", err)
	}
}

// TestResolve_PTH10_NewFileInRoot checks a new file in a missing subdirectory
// of a root is allowed.
func TestResolve_PTH10_NewFileInRoot(t *testing.T) {
	root := t.TempDir()
	ctx := &tool.Context{WorkDir: root, Roots: []string{root}}
	if _, err := tool.ResolvePath(ctx, "a/b/c.txt"); err != nil {
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
	if _, err := tool.ResolvePath(ctx, "link/new.txt"); err == nil || !strings.Contains(err.Error(), "outside the allowed directories") {
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
	got, err := tool.ResolvePath(&tool.Context{}, "a.txt")
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
	got, err := tool.ResolvePath(&tool.Context{WorkDir: dir}, "dir é/ファイル.txt")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasPrefix(got, dir) {
		t.Fatalf("resolve = %q, want a path under %q", got, dir)
	}
}

// TestResolve_PTH14_FilesystemRoot checks a root of "/" contains every path.
func TestResolve_PTH14_FilesystemRoot(t *testing.T) {
	dir := t.TempDir()
	ctx := &tool.Context{WorkDir: dir, Roots: []string{string(filepath.Separator)}}
	if _, err := tool.ResolvePath(ctx, "a.txt"); err != nil {
		t.Fatalf("ResolvePath under the filesystem root: %v", err)
	}
}

// TestResolve_PTH15_SiblingPrefix checks a directory whose name starts with a
// root's name is not inside that root, while a name that merely starts with
// dots is.
func TestResolve_PTH15_SiblingPrefix(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "work")
	sibling := filepath.Join(base, "work-secrets")
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	ctx := &tool.Context{WorkDir: root, Roots: []string{root}}
	if _, err := tool.ResolvePath(ctx, filepath.Join(sibling, "key")); err == nil {
		t.Fatal("a sibling directory sharing the root's name prefix must be refused")
	}
	if _, err := tool.ResolvePath(ctx, "..foo/bar"); err != nil {
		t.Fatalf("a name that merely starts with dots is inside the root: %v", err)
	}
}

// TestResolve_PTH16_EveryToolHonoursRoots checks each tool refuses a path outside
// the roots and a symlink inside them that leads out, and that write creates
// nothing in either case.
func TestResolve_PTH16_EveryToolHonoursRoots(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	writeFile(t, filepath.Join(outside, "secret.txt"), "top secret")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	testkit.Symlink(t, root, "escape", outside)
	ctx := &tool.Context{WorkDir: root, Roots: []string{root}}

	for _, target := range []string{"../outside/secret.txt", "escape/secret.txt"} {
		calls := map[string]struct {
			tl   tool.Tool
			args map[string]any
		}{
			"read":  {Read(), map[string]any{"path": target}},
			"list":  {List(), map[string]any{"path": filepath.Dir(target)}},
			"write": {Write(), map[string]any{"path": target, "content": "pwned"}},
			"edit":  {Edit(), map[string]any{"path": target, "edits": []map[string]string{{"oldText": "top", "newText": "x"}}}},
		}
		for name, c := range calls {
			res := run(t, c.tl, ctx, c.args)
			if !res.IsError || !strings.Contains(res.Content, "outside the allowed directories") {
				t.Errorf("%s %q: result = %+v, want the outside-roots error", name, target, res)
			}
			if strings.Contains(res.Content, "top secret") {
				t.Errorf("%s %q leaked the secret: %q", name, target, res.Content)
			}
		}
	}
	if got := readFile(t, filepath.Join(outside, "secret.txt")); got != "top secret" {
		t.Fatalf("outside file = %q, want it untouched", got)
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
		got, err := tool.ResolvePath(&tool.Context{WorkDir: t.TempDir()}, path)
		if err != nil {
			return
		}
		if !filepath.IsAbs(got) || got != filepath.Clean(got) {
			t.Fatalf("tool.ResolvePath(%q) = %q, want an absolute clean path", path, got)
		}
	})
}
