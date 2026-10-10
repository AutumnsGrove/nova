package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNoKey means no credential is stored.
var ErrNoKey = errors.New("auth: no stored key")

// Store persists one OpenRouter API key.
type Store interface {
	// Load returns the stored key, or ErrNoKey when none is stored. A file
	// that is readable but more permissive than 0600 still loads, with a
	// non-nil *PermissionWarning alongside the key.
	Load() (key string, err error)
	// Save writes the key atomically, replacing any previous value.
	Save(key string) error
	// Delete removes the stored key. Deleting a missing key is not an error.
	Delete() error
}

// PermissionWarning reports that the key store file is readable by other
// users. It is returned alongside the loaded key, not instead of it, so the
// caller can warn without losing the credential.
type PermissionWarning struct {
	Path string
	Mode os.FileMode
}

// Error implements error. It names the path and mode but never the key.
func (w *PermissionWarning) Error() string {
	return fmt.Sprintf("auth: key store %s has mode %04o, more permissive than 0600", w.Path, w.Mode)
}

// fileFormat is the on-disk JSON shape.
type fileFormat struct {
	OpenRouterKey string `json:"openrouter_key"`
}

// FileStore is the JSON key store used by `nova login`.
type FileStore struct {
	path string
	err  error
	// rename is injectable so a test can prove Save is atomic when the final
	// rename fails. It defaults to os.Rename.
	rename func(oldpath, newpath string) error
}

// NewFileStore returns a store at path. An empty path uses
// os.UserConfigDir()/nova/auth.json, the same location the docs describe.
func NewFileStore(path string) Store {
	s := &FileStore{path: path}
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			s.err = fmt.Errorf("auth: locate config directory: %w", err)
		} else {
			s.path = filepath.Join(dir, "nova", "auth.json")
		}
	}
	return s
}

// Load reads the key. It returns ErrNoKey when the file does not exist.
func (s *FileStore) Load() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	info, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoKey
	}
	if err != nil {
		return "", fmt.Errorf("auth: stat key store: %w", err)
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoKey
	}
	if err != nil {
		return "", fmt.Errorf("auth: read key store: %w", err)
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		// The message must not quote the file: it could hold a key.
		return "", fmt.Errorf("auth: key store %s is not valid JSON", s.path)
	}
	if f.OpenRouterKey == "" {
		return "", ErrNoKey
	}
	var warn error
	if perm := info.Mode().Perm(); perm&^os.FileMode(0o600) != 0 {
		warn = &PermissionWarning{Path: s.path, Mode: perm}
	}
	return f.OpenRouterKey, warn
}

// Save writes the key atomically: a 0600 temporary file in the same directory
// is renamed over the target, which is created along with a 0700 directory.
func (s *FileStore) Save(key string) error {
	if s.err != nil {
		return s.err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("auth: create key store directory: %w", err)
	}
	data, err := json.Marshal(fileFormat{OpenRouterKey: key})
	if err != nil {
		return fmt.Errorf("auth: encode key store: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return fmt.Errorf("auth: create temporary key store: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("auth: secure temporary key store: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("auth: write temporary key store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("auth: close temporary key store: %w", err)
	}
	rename := s.rename
	if rename == nil {
		rename = os.Rename
	}
	if err := rename(tmpName, s.path); err != nil {
		return fmt.Errorf("auth: replace key store: %w", err)
	}
	tmpName = "" // consumed by rename
	return nil
}

// Delete removes the stored key. A missing file is not an error.
func (s *FileStore) Delete() error {
	if s.err != nil {
		return s.err
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("auth: delete key store: %w", err)
	}
	return nil
}
