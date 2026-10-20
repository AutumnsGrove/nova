package config

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestTrust_TRU12_AncestorOfHomeTooBroad checks that trusting a directory that
// contains the home directory is refused like trusting home itself: trust is
// inherited by every descendant, so it would cover every project under home.
func TestTrust_TRU12_AncestorOfHomeTooBroad(t *testing.T) {
	home := filepath.Join(t.TempDir(), "users", "me")
	write(t, filepath.Join(home, "marker"), "x")
	t.Setenv("HOME", home)
	store, path := trustAt(t)
	for _, dir := range []string{filepath.Dir(home), filepath.Dir(filepath.Dir(home))} {
		if err := store.Trust(dir); !errors.Is(err, ErrTooBroad) {
			t.Errorf("Trust(%s) = %v, want ErrTooBroad", dir, err)
		}
	}
	// A project below home is fine, and the refused calls wrote nothing.
	project := filepath.Join(home, "work", "app")
	write(t, filepath.Join(project, "marker"), "x")
	if err := store.Trust(project); err != nil {
		t.Fatalf("Trust(project) = %v", err)
	}
	if !store.IsTrusted(project) || store.IsTrusted(filepath.Dir(home)) {
		t.Errorf("trust state wrong after the calls; store file %s", path)
	}
}
