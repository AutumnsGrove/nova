package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/tool"
)

// listNames returns the entry names from a list result, in order.
func listNames(content string) []string {
	var names []string
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			names = append(names, fields[len(fields)-1])
		}
	}
	return names
}

func TestList_LST01_Sorted(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.txt", "a.txt", "c.txt"} {
		writeFile(t, filepath.Join(dir, name), "x")
	}
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "."})
	names := listNames(res.Content)
	if strings.Join(names, ",") != "a.txt,b.txt,c.txt" {
		t.Fatalf("names = %v, want sorted", names)
	}
}

func TestList_LST02_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "only.txt"), "x")
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{})
	if !strings.Contains(res.Content, "only.txt") {
		t.Fatalf("content = %q, want the WorkDir listing", res.Content)
	}
}

func TestList_LST03_Pattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "x")
	writeFile(t, filepath.Join(dir, "b.txt"), "x")
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": ".", "pattern": "*.go"})
	if !strings.Contains(res.Content, "a.go") || strings.Contains(res.Content, "b.txt") {
		t.Fatalf("content = %q, want only a.go", res.Content)
	}
}

func TestList_LST04_InvalidPattern(t *testing.T) {
	dir := t.TempDir()
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": ".", "pattern": "["})
	if !res.IsError || !strings.Contains(res.Content, "invalid pattern") {
		t.Fatalf("result = %+v, want an invalid pattern error", res)
	}
}

func TestList_LST05_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "x")
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "a.txt"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

func TestList_LST06_Missing(t *testing.T) {
	dir := t.TempDir()
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "missing"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

func TestList_LST07_DirectoriesSlash(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "."})
	if !strings.Contains(res.Content, "sub/") {
		t.Fatalf("content = %q, want sub/", res.Content)
	}
}

func TestList_LST08_SymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "real.txt"), "x")
	if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "."})
	if !strings.Contains(res.Content, "link.txt -> real.txt") {
		t.Fatalf("content = %q, want the target", res.Content)
	}
}

func TestList_LST09_DanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(dir, "dangling")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "."})
	if !strings.Contains(res.Content, "dangling -> missing") || !strings.Contains(res.Content, "?") {
		t.Fatalf("content = %q, want the dangling link shown with ?", res.Content)
	}
}

func TestList_LST10_Hidden(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".hidden"), "x")
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "."})
	if !strings.Contains(res.Content, ".hidden") {
		t.Fatalf("content = %q, want the hidden file", res.Content)
	}
}

func TestList_LST11_Cap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxListEntries+300; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%04d", i)), "x")
	}
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "."})
	if !res.Truncated || !strings.Contains(res.Content, "300 more entries omitted") {
		t.Fatalf("result = %+v, want a cap of %d and 300 omitted", res, maxListEntries)
	}
	if got := strings.Count(res.Content, "\n"); got != maxListEntries+1 {
		t.Fatalf("rows = %d, want %d plus the trailer", got, maxListEntries)
	}
}

func TestList_LST12_Empty(t *testing.T) {
	dir := t.TempDir()
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "."})
	if res.Content != "(no entries)" {
		t.Fatalf("content = %q, want (no entries)", res.Content)
	}
}

func TestList_LST13_BytewiseSort(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"é", "a", "B"} {
		writeFile(t, filepath.Join(dir, name), "x")
	}
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "."})
	names := listNames(res.Content)
	if strings.Join(names, ",") != "B,a,é" {
		t.Fatalf("names = %v, want bytewise B,a,é", names)
	}
}

func TestList_LST14_PermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o000); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })
	res := run(t, List(), &tool.Context{WorkDir: dir}, map[string]any{"path": "sub"})
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

func TestList_LST15_Annotations(t *testing.T) {
	info := List().Info()
	if !info.ReadOnly || !info.Parallel {
		t.Fatalf("annotations = %+v, want read-only and parallel", info)
	}
}
