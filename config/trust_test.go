package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// trustAt returns a store persisted under fresh temp dir and a home dir.
func trustAt(t *testing.T) (TrustStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trust.json")
	return NewTrustStore(path), path
}

func TestTrust_TRU01_UntrustedByDefault(t *testing.T) {
	store, _ := trustAt(t)
	if store.IsTrusted(t.TempDir()) {
		t.Fatal("a fresh store should trust nothing")
	}
}

func TestTrust_TRU02_TrustThenCheck(t *testing.T) {
	store, _ := trustAt(t)
	dir := t.TempDir()
	if err := store.Trust(dir); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if !store.IsTrusted(dir) {
		t.Fatal("trusted directory should be trusted")
	}
}

func TestTrust_TRU03_AncestorTrusted(t *testing.T) {
	store, _ := trustAt(t)
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(root); err != nil {
		t.Fatal(err)
	}
	if !store.IsTrusted(deep) {
		t.Fatal("a child of a trusted directory should be trusted")
	}
}

func TestTrust_TRU04_SiblingNotTrusted(t *testing.T) {
	store, _ := trustAt(t)
	root := t.TempDir()
	other := filepath.Join(root, "c")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	if store.IsTrusted(other) {
		t.Fatal("a sibling should not borrow trust")
	}
	if store.IsTrusted(root + "b") {
		t.Fatal("a shared prefix should not count as an ancestor")
	}
}

func TestTrust_TRU05_SymlinkCannotBorrowTrust(t *testing.T) {
	store, _ := trustAt(t)
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(real); err != nil {
		t.Fatal(err)
	}
	if !store.IsTrusted(link) {
		t.Fatal("a symlink to a trusted directory should resolve to it")
	}

	otherReal := filepath.Join(base, "other-real")
	if err := os.Mkdir(otherReal, 0o755); err != nil {
		t.Fatal(err)
	}
	otherLink := filepath.Join(base, "other-link")
	if err := os.Symlink(otherReal, otherLink); err != nil {
		t.Fatal(err)
	}
	if store.IsTrusted(otherLink) {
		t.Fatal("a symlink to an untrusted directory should not be trusted")
	}
}

func TestTrust_TRU06_Revoke(t *testing.T) {
	store, _ := trustAt(t)
	dir := t.TempDir()
	if err := store.Trust(dir); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(dir); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if store.IsTrusted(dir) {
		t.Fatal("revoked directory should not be trusted")
	}
	if err := store.Revoke(t.TempDir()); err != nil {
		t.Fatalf("revoking an unknown directory: %v", err)
	}
}

func TestTrust_TRU07_CorruptFile(t *testing.T) {
	store, path := trustAt(t)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store.IsTrusted(t.TempDir()) {
		t.Fatal("a corrupt store should trust nothing")
	}
	dir := t.TempDir()
	if err := store.Trust(dir); err != nil {
		t.Fatalf("Trust after corrupt: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	if err := json.Unmarshal(data, &dirs); err != nil {
		t.Fatalf("store was not rewritten with valid JSON: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("dirs = %v, want one entry", dirs)
	}
}

func TestTrust_TRU08_ModeAndAtomicWrite(t *testing.T) {
	store, path := trustAt(t)
	first := t.TempDir()
	if err := store.Trust(first); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}

	old := renameFile
	renameFile = func(string, string) error { return errors.New("forced") }
	defer func() { renameFile = old }()
	if err := store.Trust(t.TempDir()); err == nil {
		t.Fatal("a failed rename should surface an error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	if err := json.Unmarshal(data, &dirs); err != nil {
		t.Fatalf("old file should be intact: %v", err)
	}
	if len(dirs) != 1 || dirs[0] != realPath(first) {
		t.Fatalf("dirs = %v, want only the first entry", dirs)
	}
}

func TestTrust_TRU09_TooBroad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store, _ := trustAt(t)
	if err := store.Trust("/"); !errors.Is(err, ErrTooBroad) {
		t.Fatalf("Trust(/) = %v, want ErrTooBroad", err)
	}
	if err := store.Trust(home); !errors.Is(err, ErrTooBroad) {
		t.Fatalf("Trust(home) = %v, want ErrTooBroad", err)
	}
}

func TestTrust_TRU10_RelativePath(t *testing.T) {
	store, _ := trustAt(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.Trust("sub"); err != nil {
		t.Fatal(err)
	}
	if !store.IsTrusted(filepath.Join(dir, "sub")) {
		t.Fatal("a relative trust should be stored absolute")
	}
}

func TestTrust_TRU11_ConcurrentTrusts(t *testing.T) {
	store, path := trustAt(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		dir := t.TempDir()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Trust(dir); err != nil {
				t.Errorf("Trust: %v", err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	if err := json.Unmarshal(data, &dirs); err != nil {
		t.Fatalf("concurrent writes corrupted the file: %v", err)
	}
	if len(dirs) != 20 {
		t.Fatalf("dirs = %d, want 20", len(dirs))
	}
}
