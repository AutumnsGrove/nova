package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// TempTree writes files (creating parent directories) under the test's temp
// directory and returns that root.
func TempTree(t testing.TB, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("testkit: mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("testkit: write %s: %v", name, err)
		}
	}
	return root
}

// Symlink creates a symlink at root/name pointing at target.
func Symlink(t testing.TB, root, name, target string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("testkit: mkdir for symlink %s: %v", name, err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("testkit: symlink %s -> %s: %v", name, target, err)
	}
}

// WriteBinary writes a file of size bytes filled with NULs.
func WriteBinary(t testing.TB, root, name string, size int) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("testkit: mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("testkit: write binary %s: %v", name, err)
	}
}
