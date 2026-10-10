package ext

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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

// readLock loads the lockfile at path. A missing file is an empty lock; a
// corrupt file is an empty lock with a warning.
func readLock(path string) (map[string]LockEntry, []string) {
	out := map[string]LockEntry{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out, nil
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
	sortLockEntries(lf.Extensions)
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
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
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

// sortLockEntries orders entries by name so the file is deterministic.
func sortLockEntries(entries []LockEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Name < entries[j-1].Name; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

// manifestHash hashes a manifest file's bytes for change detection.
func manifestHash(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "extension.yaml"))
	if err != nil {
		return ""
	}
	return hashBytes(data)
}

// hashBytes returns the hex SHA-256 of b.
func hashBytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
