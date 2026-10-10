package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFS_TKF01_TempTreeCreatesNestedFiles(t *testing.T) {
	root := TempTree(t, map[string]string{"a/b/c.txt": "content"})
	got, err := os.ReadFile(filepath.Join(root, "a", "b", "c.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "content" {
		t.Errorf("content = %q, want content", got)
	}
}

func TestFS_TKF02_TempTreeUnderTempDir(t *testing.T) {
	root := TempTree(t, map[string]string{"x.txt": "x"})
	if !filepath.IsAbs(root) {
		t.Errorf("root = %q, want an absolute path", root)
	}
	temp := filepath.Clean(os.TempDir())
	if !strings.HasPrefix(root, temp+string(os.PathSeparator)) {
		t.Errorf("root = %q, want it under the temp dir %q", root, temp)
	}
}

func TestFS_TKF03_SymlinkAndWriteBinary(t *testing.T) {
	root := TempTree(t, map[string]string{"target.txt": "target"})
	Symlink(t, root, "link.txt", "target.txt")
	resolved, err := os.ReadFile(filepath.Join(root, "link.txt"))
	if err != nil {
		t.Fatalf("read symlink: %v", err)
	}
	if string(resolved) != "target" {
		t.Errorf("symlink content = %q, want target", resolved)
	}

	WriteBinary(t, root, "bin.dat", 1024)
	data, err := os.ReadFile(filepath.Join(root, "bin.dat"))
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if len(data) != 1024 {
		t.Errorf("binary size = %d, want 1024", len(data))
	}
	if !strings.ContainsRune(string(data), 0) {
		t.Error("binary file has no NUL bytes")
	}
}
