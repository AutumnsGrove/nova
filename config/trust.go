package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// ErrTooBroad is returned by Trust for a directory that would grant trust to
// unbounded territory: the filesystem root or the home directory.
var ErrTooBroad = errors.New("config: directory is too broad to trust")

// TrustStore records which project directories the user has trusted. A
// directory is trusted when it or any ancestor was added.
type TrustStore interface {
	IsTrusted(dir string) bool
	Trust(dir string) error
	Revoke(dir string) error
}

// fileTrustStore is a TrustStore backed by a JSON file.
type fileTrustStore struct {
	path string
	home string

	mu     sync.Mutex
	dirs   []string
	loaded bool
}

// NewTrustStore returns a store persisted at path.
func NewTrustStore(path string) TrustStore {
	home, _ := os.UserHomeDir()
	return &fileTrustStore{path: path, home: home}
}

// IsTrusted reports whether dir or one of its ancestors was trusted. Symlinks
// are resolved first, so a link cannot borrow another directory's trust.
func (s *fileTrustStore) IsTrusted(dir string) bool {
	real := realPath(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	for _, entry := range s.dirs {
		if isSameOrAncestor(realPath(entry), real) {
			return true
		}
	}
	return false
}

// Trust adds dir. It refuses the filesystem root and the home directory.
func (s *fileTrustStore) Trust(dir string) error {
	real := realPath(dir)
	if real == string(filepath.Separator) {
		return fmt.Errorf("%w: %s", ErrTooBroad, real)
	}
	if s.home != "" && real == realPath(s.home) {
		return fmt.Errorf("%w: %s", ErrTooBroad, real)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if !slices.Contains(s.dirs, real) {
		s.dirs = append(s.dirs, real)
	}
	return s.writeLocked()
}

// Revoke removes dir. Revoking something that was never trusted is not an error.
func (s *fileTrustStore) Revoke(dir string) error {
	real := realPath(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	filtered := s.dirs[:0]
	for _, entry := range s.dirs {
		if realPath(entry) != real {
			filtered = append(filtered, entry)
		}
	}
	s.dirs = filtered
	return s.writeLocked()
}

// loadLocked reads the file once. A missing or corrupt file counts as empty.
func (s *fileTrustStore) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	data, err := readRegularFile(s.path, maxReadBytes)
	if err != nil {
		return
	}
	var dirs []string
	if err := json.Unmarshal(data, &dirs); err != nil {
		// Corrupt: treat as empty and let the next Trust rewrite it.
		s.dirs = nil
		return
	}
	s.dirs = dirs
}

// writeLocked replaces the file atomically with mode 0600.
func (s *fileTrustStore) writeLocked() error {
	data, err := json.Marshal(s.dirs)
	if err != nil {
		return fmt.Errorf("config: trust encode: %w", err)
	}
	return writeFileAtomic(s.path, data, "trust store")
}

// realPath cleans a path to absolute and resolves symlinks where it can.
func realPath(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// isSameOrAncestor reports whether ancestor is path or a parent of it.
func isSameOrAncestor(ancestor, path string) bool {
	if ancestor == path {
		return true
	}
	return strings.HasPrefix(path, ancestor+string(filepath.Separator))
}
