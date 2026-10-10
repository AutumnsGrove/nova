package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/tool"
)

// TestWrite_WRT01_Create checks a new file is created and reported as created.
func TestWrite_WRT01_Create(t *testing.T) {
	dir := t.TempDir()
	ctx := &tool.Context{WorkDir: dir}
	res := run(t, Write(), ctx, map[string]any{"path": "new.txt", "content": "hi"})
	if res.IsError || !strings.Contains(res.Content, "created") {
		t.Fatalf("result = %+v, want created", res)
	}
	if got := readFile(t, filepath.Join(dir, "new.txt")); got != "hi" {
		t.Fatalf("content = %q, want hi", got)
	}
}

// TestWrite_WRT02_Overwrite checks an existing file is replaced.
func TestWrite_WRT02_Overwrite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "old")
	res := run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "content": "new"})
	if res.IsError || !strings.Contains(res.Content, "overwrote") {
		t.Fatalf("result = %+v, want overwrote", res)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "new" {
		t.Fatalf("content = %q, want new", got)
	}
}

// TestWrite_WRT03_Parents checks missing parent directories are created 0755.
func TestWrite_WRT03_Parents(t *testing.T) {
	dir := t.TempDir()
	run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a/b/c.txt", "content": "x"})
	info, err := os.Stat(filepath.Join(dir, "a", "b"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("dir mode = %04o, want 0755", got)
	}
}

// TestWrite_WRT04_Empty checks empty content creates an empty file.
func TestWrite_WRT04_Empty(t *testing.T) {
	dir := t.TempDir()
	run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "e.txt", "content": ""})
	info, err := os.Stat(filepath.Join(dir, "e.txt"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("size = %d, want 0", info.Size())
	}
}

// TestWrite_WRT05_TooBig checks content over the cap is refused before writing.
func TestWrite_WRT05_TooBig(t *testing.T) {
	dir := t.TempDir()
	res := run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "big.txt", "content": strings.Repeat("x", maxFileBytes+1)})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "big.txt")); !os.IsNotExist(err) {
		t.Fatal("file was created despite the size error")
	}
}

// TestWrite_WRT06_Directory checks writing to a directory is refused.
func TestWrite_WRT06_Directory(t *testing.T) {
	dir := t.TempDir()
	res := run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": ".", "content": "x"})
	if !res.IsError || !strings.Contains(res.Content, "is a directory") {
		t.Fatalf("result = %+v, want is a directory", res)
	}
}

// TestWrite_WRT07_PreservesMode checks an existing file's mode survives.
func TestWrite_WRT07_PreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.sh")
	writeFile(t, path, "old")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "s.sh", "content": "new"})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %04o, want 0755", got)
	}
}

// TestWrite_WRT08_NewFileMode checks a new file is 0644.
func TestWrite_WRT08_NewFileMode(t *testing.T) {
	dir := t.TempDir()
	run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "n.txt", "content": "x"})
	info, err := os.Stat(filepath.Join(dir, "n.txt"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %04o, want 0644", got)
	}
}

// TestWrite_WRT09_AtomicOnFailure checks a failed write leaves the original and
// no temporary file.
func TestWrite_WRT09_AtomicOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "original")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	res := run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "content": "new"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
	if got := readFile(t, path); got != "original" {
		t.Fatalf("content = %q, want original", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".nova-") {
			t.Fatalf("temporary file %q left behind", e.Name())
		}
	}
}

// TestWrite_WRT10_NoTemp checks a successful write leaves only the target.
func TestWrite_WRT10_NoTemp(t *testing.T) {
	dir := t.TempDir()
	run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "content": "x"})
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Fatalf("entries = %v, want only a.txt", entries)
	}
}

// TestWrite_WRT11_ThroughSymlink checks a symlink target is updated, not the
// link.
func TestWrite_WRT11_ThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	writeFile(t, real, "old")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "link.txt", "content": "new"})
	if got := readFile(t, real); got != "new" {
		t.Fatalf("real content = %q, want new", got)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
}

// TestWrite_WRT12_DanglingSymlink checks a dangling link is an error.
func TestWrite_WRT12_DanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	res := run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "dangling", "content": "x"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

// TestWrite_WRT13_ParentIsFile checks a file parent is an error.
func TestWrite_WRT13_ParentIsFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "file.txt"), "x")
	res := run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "file.txt/child", "content": "x"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

// TestWrite_WRT14_Concurrent checks concurrent writes to one path are
// serialised and leave one writer's content.
func TestWrite_WRT14_Concurrent(t *testing.T) {
	dir := t.TempDir()
	ctx := &tool.Context{WorkDir: dir}
	const writers = 20
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]any{"path": "a.txt", "content": strings.Repeat("x", i+1)})
			if res, err := Write().Run(context.Background(), raw, ctx); err != nil || res.IsError {
				t.Errorf("write %d: res=%+v err=%v", i, res, err)
			}
		}(i)
	}
	wg.Wait()
	got := readFile(t, filepath.Join(dir, "a.txt"))
	if got == "" || strings.Trim(got, "x") != "" {
		t.Fatalf("content = %q, want one writer's run of x", got)
	}
}

// TestWrite_WRT15_Details checks the structured details.
func TestWrite_WRT15_Details(t *testing.T) {
	dir := t.TempDir()
	res := run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt", "content": "abc"})
	var details struct {
		Path    string `json:"path"`
		Created bool   `json:"created"`
		Bytes   int    `json:"bytes"`
	}
	if err := json.Unmarshal(res.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if !details.Created || details.Bytes != 3 || !strings.HasSuffix(details.Path, "a.txt") {
		t.Fatalf("details = %+v", details)
	}
}

// TestWrite_WRT16_Bytes checks CRLF and NUL bytes are written unchanged.
func TestWrite_WRT16_Bytes(t *testing.T) {
	dir := t.TempDir()
	content := "a\r\nb\x00c"
	run(t, Write(), &tool.Context{WorkDir: dir}, map[string]any{"path": "b.bin", "content": content})
	if got := readFile(t, filepath.Join(dir, "b.bin")); got != content {
		t.Fatalf("content = %q, want %q", got, content)
	}
}

// TestWrite_WRT17_Annotations checks the tool's annotations.
func TestWrite_WRT17_Annotations(t *testing.T) {
	info := Write().Info()
	if !info.Destructive || info.Parallel {
		t.Fatalf("annotations = %+v, want destructive and not parallel", info)
	}
}
