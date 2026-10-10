package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// storePath returns a path in a fresh nested directory so Save creates the
// directory itself.
func storePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nova", "auth.json")
}

// TestStore_STO01_SaveLoad checks a saved key loads back.
func TestStore_STO01_SaveLoad(t *testing.T) {
	s := NewFileStore(storePath(t))
	if err := s.Save("secret-key"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	key, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if key != "secret-key" {
		t.Fatalf("Load() = %q, want secret-key", key)
	}
}

// TestStore_STO02_FileModes checks the file is 0600 and its directory 0700.
func TestStore_STO02_FileModes(t *testing.T) {
	path := storePath(t)
	s := NewFileStore(path)
	if err := s.Save("k"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %04o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %04o, want 0700", got)
	}
}

// TestStore_STO03_LoadMissing checks a missing file is ErrNoKey.
func TestStore_STO03_LoadMissing(t *testing.T) {
	_, err := NewFileStore(storePath(t)).Load()
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("Load() error = %v, want ErrNoKey", err)
	}
}

// TestStore_STO04_SaveAtomic checks a failed final rename leaves the previous
// key intact and removes the temporary file.
func TestStore_STO04_SaveAtomic(t *testing.T) {
	path := storePath(t)
	s := NewFileStore(path)
	if err := s.Save("first"); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	fs := s.(*FileStore)
	fs.rename = func(string, string) error { return errors.New("rename refused") }
	if err := s.Save("second"); err == nil {
		t.Fatal("Save second succeeded, want failure")
	}
	key, err := s.Load()
	if err != nil {
		t.Fatalf("Load after failed save: %v", err)
	}
	if key != "first" {
		t.Fatalf("Load() = %q, want first", key)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".auth-") {
			t.Errorf("temporary file %q left behind", e.Name())
		}
	}
}

// TestStore_STO05_PermissionWarning checks a permissive file still loads but
// warns.
func TestStore_STO05_PermissionWarning(t *testing.T) {
	path := storePath(t)
	s := NewFileStore(path)
	if err := s.Save("k"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	key, err := s.Load()
	if key != "k" {
		t.Fatalf("Load() = %q, want k", key)
	}
	var warn *PermissionWarning
	if !errors.As(err, &warn) {
		t.Fatalf("Load() error = %v, want *PermissionWarning", err)
	}
}

// TestStore_STO06_DeleteMissing checks deleting a missing key is fine.
func TestStore_STO06_DeleteMissing(t *testing.T) {
	if err := NewFileStore(storePath(t)).Delete(); err != nil {
		t.Fatalf("Delete() = %v, want nil", err)
	}
}

// TestStore_STO07_Corrupt checks a non-JSON file errors without quoting it.
func TestStore_STO07_Corrupt(t *testing.T) {
	path := storePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const marker = "not-json-marker-content"
	if err := os.WriteFile(path, []byte(marker), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := NewFileStore(path).Load()
	if err == nil {
		t.Fatal("Load() = nil, want error")
	}
	if strings.Contains(err.Error(), marker) {
		t.Errorf("error quotes the file contents: %v", err)
	}
}

// TestStore_STO08_KeyNotInErrors checks the key never appears in any error.
func TestStore_STO08_KeyNotInErrors(t *testing.T) {
	const key = "sk-super-secret-value"
	path := storePath(t)

	missing, err := NewFileStore(path).Load()
	if err != nil && strings.Contains(err.Error(), key) {
		t.Errorf("missing-file error leaked the key: %v", err)
	}
	_ = missing

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A corrupt file that literally contains the key must not leak it.
	if err := os.WriteFile(path, []byte(`{"openrouter_key":"`+key), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := NewFileStore(path).Load(); err == nil || strings.Contains(err.Error(), key) {
		t.Errorf("corrupt-file error = %v, leaked key or was nil", err)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if err := NewFileStore(path).Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if _, err := NewFileStore(path).Load(); err != nil && strings.Contains(err.Error(), key) {
		t.Errorf("permission warning leaked the key: %v", err)
	}
}

// TestStore_STO09_ConcurrentSaves checks concurrent writers never corrupt the
// file: a reader always sees either version or a valid JSON document.
func TestStore_STO09_ConcurrentSaves(t *testing.T) {
	path := storePath(t)
	s := NewFileStore(path)

	const writers = 10
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.Save(strings.Repeat("key", i+1))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("file is not valid JSON: %q", raw)
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}
