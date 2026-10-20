package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// renameFile is a package variable so a test can force a failed rename.
var renameFile = os.Rename

// writeFileAtomic replaces path with data. The bytes go to a 0600 temporary file
// in the same directory (created, with the directory at 0700, before any secret
// byte is written), which is synced and renamed over the target. A failure at
// any step leaves the previous file untouched and removes the temporary file.
// what names the file in errors.
func writeFileAtomic(path string, data []byte, what string) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create %s directory: %w", what, err)
	}
	tmp, err := os.CreateTemp(dir, ".nova-*.tmp")
	if err != nil {
		return fmt.Errorf("config: create temporary %s: %w", what, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	// CreateTemp already uses 0600; this makes the guarantee explicit and
	// independent of the umask or the platform's default.
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: secure temporary %s: %w", what, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write temporary %s: %w", what, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: sync temporary %s: %w", what, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temporary %s: %w", what, err)
	}
	if err := renameFile(tmpName, path); err != nil {
		return fmt.Errorf("config: replace %s: %w", what, err)
	}
	return nil
}
