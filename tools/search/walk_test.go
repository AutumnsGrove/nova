package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/tool"
)

// collect walks root and returns the visited entries.
func collect(t *testing.T, root string, opt walkOptions) []walkEntry {
	t.Helper()
	var got []walkEntry
	err := walk(context.Background(), root, opt, func(e walkEntry) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatalf("walk(%s): %v", root, err)
	}
	return got
}

// rels returns the relative paths of the visited entries.
func rels(entries []walkEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Rel)
	}
	return out
}

// files returns the relative paths of the non-directory entries.
func files(entries []walkEntry) []string {
	var out []string
	for _, e := range entries {
		if !e.IsDir {
			out = append(out, e.Rel)
		}
	}
	return out
}

func has(rels []string, want string) bool {
	for _, r := range rels {
		if r == want {
			return true
		}
	}
	return false
}

func TestWalk_WLK01_DeterministicOrder(t *testing.T) {
	dir := tree(t, map[string]string{"b": "x", "a": "x", "c/d": "x"})
	first := files(collect(t, dir, walkOptions{}))
	second := files(collect(t, dir, walkOptions{}))
	if strings.Join(first, ",") != "a,b,c/d" {
		t.Fatalf("files = %v, want a,b,c/d", first)
	}
	if strings.Join(second, ",") != strings.Join(first, ",") {
		t.Fatalf("walk is not deterministic: %v vs %v", first, second)
	}
}

func TestWalk_WLK02_GitSkipped(t *testing.T) {
	dir := tree(t, map[string]string{".git/config": "x", "a.txt": "x"})
	got := rels(collect(t, dir, walkOptions{Hidden: true}))
	for _, r := range got {
		if strings.HasPrefix(r, ".git") {
			t.Fatalf("walked %q, want .git skipped", r)
		}
	}
}

func TestWalk_WLK03_HiddenSkipped(t *testing.T) {
	dir := tree(t, map[string]string{".env": "x", ".hidden/y": "x", "a.txt": "x"})
	got := rels(collect(t, dir, walkOptions{}))
	if has(got, ".env") || has(got, ".hidden/y") {
		t.Fatalf("rels = %v, want hidden entries skipped", got)
	}
}

func TestWalk_WLK04_HiddenIncluded(t *testing.T) {
	dir := tree(t, map[string]string{".env": "x", ".hidden/y": "x"})
	got := rels(collect(t, dir, walkOptions{Hidden: true}))
	if !has(got, ".env") || !has(got, ".hidden/y") {
		t.Fatalf("rels = %v, want hidden entries included", got)
	}
}

func TestWalk_WLK05_SymlinkDirNotFollowed(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "x")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	got := rels(collect(t, dir, walkOptions{}))
	if has(got, "link") || has(got, "link/secret.txt") {
		t.Fatalf("rels = %v, want the symlink not followed", got)
	}
}

func TestWalk_WLK06_SymlinkLoopTerminates(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink("..", filepath.Join(dir, "a", "b")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// This must return rather than recurse forever.
	collect(t, dir, walkOptions{FollowSymlinks: true})
}

func TestWalk_WLK07_SymlinkFileSkippedByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "real.txt"), "x")
	if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if has(rels(collect(t, dir, walkOptions{})), "link.txt") {
		t.Fatal("a symlink to a file should be skipped by default")
	}
	if !has(rels(collect(t, dir, walkOptions{FollowSymlinks: true})), "link.txt") {
		t.Fatal("a symlink to a file should appear when following symlinks")
	}
}

func TestWalk_WLK08_ContextCancelled(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "x"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := walk(ctx, dir, walkOptions{}, func(walkEntry) error { return nil })
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWalk_WLK09_UnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(locked, "a.txt"), "x")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	var stats walkStats
	collect(t, dir, walkOptions{stats: &stats})
	if stats.unreadable != 1 {
		t.Fatalf("unreadable = %d, want 1", stats.unreadable)
	}
}

func TestWalk_WLK10_DeepTree(t *testing.T) {
	dir := t.TempDir()
	deep := strings.Repeat("d/", 200)
	writeFile(t, filepath.Join(dir, filepath.FromSlash(deep+"leaf.txt")), "x")
	got := rels(collect(t, dir, walkOptions{}))
	if !has(got, deep+"leaf.txt") {
		t.Fatal("the deep file should be found without a stack overflow")
	}
}

func TestWalk_WLK11_MaxDepth(t *testing.T) {
	dir := tree(t, map[string]string{"a/b/c/d": "x"})
	got := rels(collect(t, dir, walkOptions{MaxDepth: 2}))
	if !has(got, "a") || !has(got, "a/b") {
		t.Fatalf("rels = %v, want depth 1 and 2 present", got)
	}
	if has(got, "a/b/c") {
		t.Fatalf("rels = %v, want depth 3 absent", got)
	}
}

func TestWalk_WLK12_RootsContainment(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outside := filepath.Join(dir, "outside")
	for _, p := range []string{root, outside} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	writeFile(t, filepath.Join(outside, "secret.txt"), "x")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	ctx := &tool.Context{WorkDir: root, Roots: []string{root}}
	for _, path := range []string{"../outside", "escape"} {
		res := run(t, Glob(), ctx, map[string]any{"pattern": "**/*", "path": path})
		if !res.IsError || strings.Contains(res.Content, "secret.txt") {
			t.Errorf("glob in %q: result = %+v, want the outside-roots error", path, res)
		}
	}
	if res := run(t, Glob(), ctx, map[string]any{"pattern": "**/*"}); res.IsError || strings.Contains(res.Content, "secret.txt") {
		t.Errorf("glob in the root: result = %+v, want no error and no escape through the symlink", res)
	}
}

func TestWalk_WLK13_ExplicitFileIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(dir, "a.log"), "x")
	got := rels(collect(t, filepath.Join(dir, "a.log"), walkOptions{}))
	if strings.Join(got, ",") != "a.log" {
		t.Fatalf("rels = %v, want the explicit file searched", got)
	}
}

func TestIgnore_IGN07_NoReincludeUnderExcludedParent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "build/\n!build/keep\n")
	writeFile(t, filepath.Join(dir, "build", "keep"), "x")
	got := rels(collect(t, dir, walkOptions{}))
	if has(got, "build") || has(got, "build/keep") {
		t.Fatalf("rels = %v, want build/ excluded entirely", got)
	}
}

func TestIgnore_IGN15_NestedScope(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(dir, "sub", "a.log"), "x")
	writeFile(t, filepath.Join(dir, "x.log"), "x")
	got := rels(collect(t, dir, walkOptions{}))
	if has(got, "sub/a.log") {
		t.Fatalf("rels = %v, want sub's .gitignore applied", got)
	}
	if !has(got, "x.log") {
		t.Fatalf("rels = %v, want sub's rule scoped to sub", got)
	}
}

func TestIgnore_IGN16_NestedOverridesParent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(dir, "sub", ".gitignore"), "!keep.log\n")
	writeFile(t, filepath.Join(dir, "sub", "keep.log"), "x")
	writeFile(t, filepath.Join(dir, "sub", "other.log"), "x")
	writeFile(t, filepath.Join(dir, "keep.log"), "x")
	got := rels(collect(t, dir, walkOptions{}))
	if !has(got, "sub/keep.log") {
		t.Fatalf("rels = %v, want the nested negation to apply", got)
	}
	if has(got, "sub/other.log") || has(got, "keep.log") {
		t.Fatalf("rels = %v, want other .log files ignored", got)
	}
}

func TestIgnore_IGN18_NoIgnore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(dir, "a.log"), "x")
	if has(rels(collect(t, dir, walkOptions{})), "a.log") {
		t.Fatal("a.log should be ignored by default")
	}
	if !has(rels(collect(t, dir, walkOptions{NoIgnore: true})), "a.log") {
		t.Fatal("a.log should appear with NoIgnore")
	}
}
