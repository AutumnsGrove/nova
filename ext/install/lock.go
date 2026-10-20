package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LockEntry is one installed extension in the lockfile.
type LockEntry struct {
	Name         string    `yaml:"name"`
	Source       string    `yaml:"source"`
	Ref          string    `yaml:"ref,omitempty"`
	Commit       string    `yaml:"commit"`
	Installed    time.Time `yaml:"installed"`
	ManifestHash string    `yaml:"manifest_hash"`
	Linked       bool      `yaml:"linked,omitempty"`
}

// lockFile is the on-disk lockfile shape.
type lockFile struct {
	Extensions []LockEntry `yaml:"extensions"`
}

// readLock loads the lockfile at path. A missing file is an empty lock; one that
// cannot be read or parsed is an empty lock with a warning, which the next
// write replaces.
func readLock(path string) (map[string]LockEntry, []string) {
	out := map[string]LockEntry{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, []string{"ext: unreadable lockfile: " + err.Error()}
	}
	var lf lockFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return out, []string{"ext: corrupt lockfile: " + err.Error()}
	}
	for _, e := range lf.Extensions {
		out[e.Name] = e
	}
	return out, nil
}

// writeLock writes the lockfile atomically. A failure leaves the old file
// untouched.
func writeLock(path string, entries map[string]LockEntry) error {
	lf := lockFile{}
	for _, e := range entries {
		lf.Extensions = append(lf.Extensions, e)
	}
	slices.SortFunc(lf.Extensions, func(a, b LockEntry) int { return strings.Compare(a.Name, b.Name) })
	data, err := yaml.Marshal(lf)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lock-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// After a successful rename the temp file is gone and this is a no-op.
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// Sync before the rename, or a crash could leave the new name on an empty file.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("ext: write lockfile: %w", err)
	}
	return nil
}
