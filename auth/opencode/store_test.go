package opencode

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveKey_IgnoresStaleLooseTempFile checks the key never lands in a file
// someone else can read. The old writer used the fixed name <path>.tmp and
// os.WriteFile, which keeps the mode of a file that already exists, so a stale
// 0644 temp file from a crash made the saved key world-readable.
func TestSaveKey_IgnoresStaleLooseTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode-go.json")
	stale := path + ".tmp"
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveKey(path, "secret-key"); err != nil {
		t.Fatalf("SaveKey: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %o, want 600", info.Mode().Perm())
	}
	got, err := LoadKey(path)
	if err != nil || got != "secret-key" {
		t.Fatalf("LoadKey = (%q, %v)", got, err)
	}
}

// TestSaveKey_DirectoryModeAndNoLeftovers checks the directory is created 0700
// and a save leaves exactly one file behind.
func TestSaveKey_DirectoryModeAndNoLeftovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "opencode-go.json")
	if err := SaveKey(path, "k"); err != nil {
		t.Fatalf("SaveKey: %v", err)
	}
	if err := SaveKey(path, "k2"); err != nil {
		t.Fatalf("second SaveKey: %v", err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %o, want 700", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "opencode-go.json" {
		t.Fatalf("directory holds %v, want only opencode-go.json", entries)
	}
}
