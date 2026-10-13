package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

// userConfigPath is where SetUser writes for a given home.
func userConfigPath(home string) string {
	return filepath.Join(home, ".config", "nova", "config.yaml")
}

// readUser returns the user config file's bytes.
func readUser(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(userConfigPath(home))
	if err != nil {
		t.Fatalf("read user config: %v", err)
	}
	return string(data)
}

func TestSetUser_CFW01_NewFileModeAndDir(t *testing.T) {
	home := t.TempDir()
	if err := SetUser(home, "model", "openrouter/auto"); err != nil {
		t.Fatalf("SetUser: %v", err)
	}
	info, err := os.Stat(userConfigPath(home))
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(userConfigPath(home)))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 700", dir.Mode().Perm())
	}
	cfg, _, err := Load(LoadOptions{CWD: t.TempDir(), Home: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model != "openrouter/auto" {
		t.Fatalf("model = %q, want openrouter/auto", cfg.Model)
	}
}

func TestSetUser_CFW02_ChangeOnlyThatKey(t *testing.T) {
	home := t.TempDir()
	write(t, userConfigPath(home), "model: old\nmax_turns: 7\n")
	if err := SetUser(home, "model", "new"); err != nil {
		t.Fatalf("SetUser: %v", err)
	}
	got := readUser(t, home)
	if !strings.Contains(got, "model: new") {
		t.Fatalf("model not changed:\n%s", got)
	}
	if !strings.Contains(got, "max_turns: 7") {
		t.Fatalf("max_turns changed:\n%s", got)
	}
}

func TestSetUser_CFW03_CommentsAndOrderPreserved(t *testing.T) {
	home := t.TempDir()
	original := "# top comment\nmodel: old # inline\nmax_turns: 7\n# tail\n"
	write(t, userConfigPath(home), original)
	if err := SetUser(home, "max_turns", 9); err != nil {
		t.Fatalf("SetUser: %v", err)
	}
	got := readUser(t, home)
	for _, want := range []string{"# top comment", "model: old # inline", "# tail"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q after edit:\n%s", want, got)
		}
	}
	if strings.Index(got, "# top comment") > strings.Index(got, "model:") {
		t.Fatalf("comments reordered:\n%s", got)
	}
	if !strings.Contains(got, "max_turns: 9") {
		t.Fatalf("max_turns not updated:\n%s", got)
	}
}

func TestSetUser_CFW04_NestedDottedKey(t *testing.T) {
	home := t.TempDir()
	if err := SetUser(home, "tools.bash.timeout_seconds", 42); err != nil {
		t.Fatalf("SetUser: %v", err)
	}
	cfg, _, err := Load(LoadOptions{CWD: t.TempDir(), Home: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tools.Bash.TimeoutSeconds != 42 {
		t.Fatalf("timeout = %d, want 42", cfg.Tools.Bash.TimeoutSeconds)
	}
}

func TestSetUser_CFW05_UnknownKey(t *testing.T) {
	home := t.TempDir()
	if err := SetUser(home, "nonsense.key", 1); err == nil {
		t.Fatal("SetUser accepted an unknown key")
	}
	if _, err := os.Stat(userConfigPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown key wrote a file: %v", err)
	}
}

func TestSetUser_CFW06_CorruptFileUntouched(t *testing.T) {
	home := t.TempDir()
	corrupt := "model: [unclosed\n"
	write(t, userConfigPath(home), corrupt)
	if err := SetUser(home, "model", "x"); err == nil {
		t.Fatal("SetUser accepted a corrupt file")
	}
	if got := readUser(t, home); got != corrupt {
		t.Fatalf("corrupt file changed:\n%q", got)
	}
}

func TestSetUser_CFW07_AtomicWriteFailureKeepsOriginal(t *testing.T) {
	home := t.TempDir()
	original := "model: old\n"
	write(t, userConfigPath(home), original)
	restore := renameFile
	renameFile = func(_, _ string) error { return errors.New("boom") }
	defer func() { renameFile = restore }()

	if err := SetUser(home, "model", "new"); err == nil {
		t.Fatal("SetUser ignored a rename failure")
	}
	if got := readUser(t, home); got != original {
		t.Fatalf("original changed on failed rename:\n%q", got)
	}
	// No temp files are left behind.
	entries, _ := os.ReadDir(filepath.Dir(userConfigPath(home)))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".config-") {
			t.Fatalf("left temp file %s", e.Name())
		}
	}
}

func TestSetUser_CFW08_ValueTypes(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		key   string
		value any
	}{
		{"max_turns", 12},
		{"model", "some/model"},
		{"extensions.disable", []string{"tasks", "plan"}},
	}
	for _, c := range cases {
		if err := SetUser(home, c.key, c.value); err != nil {
			t.Fatalf("SetUser(%s): %v", c.key, err)
		}
	}
	cfg, _, err := Load(LoadOptions{CWD: t.TempDir(), Home: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxTurns != 12 || cfg.Model != "some/model" {
		t.Fatalf("scalars wrong: max_turns=%d model=%q", cfg.MaxTurns, cfg.Model)
	}
	if len(cfg.Extensions.Disable) != 2 || cfg.Extensions.Disable[0] != "tasks" {
		t.Fatalf("list wrong: %v", cfg.Extensions.Disable)
	}
}

func TestSetUser_CFW09_ConcurrentCalls(t *testing.T) {
	home := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := SetUser(home, "max_turns", n); err != nil {
				t.Errorf("SetUser: %v", err)
			}
		}(i)
	}
	wg.Wait()
	cfg, _, err := Load(LoadOptions{CWD: t.TempDir(), Home: home})
	if err != nil {
		t.Fatalf("Load after concurrent writes: %v", err)
	}
	if cfg.MaxTurns < 0 || cfg.MaxTurns >= 20 {
		t.Fatalf("max_turns = %d, want one of 0..19", cfg.MaxTurns)
	}
}

func TestSetUser_CFW10_SecretsRefused(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"api_key", "token", "provider.openrouter.api_key", "auth_token"} {
		if err := SetUser(home, key, "secret"); err == nil {
			t.Fatalf("SetUser accepted secret key %q", key)
		}
	}
}

// FuzzSetUser asserts that an arbitrary dotted key and value never panic and
// never leave a partially written config: either the write succeeds and the
// file parses, or it fails and the original bytes are untouched.
func FuzzSetUser(f *testing.F) {
	f.Add("model", "openrouter/auto")
	f.Add("tools.bash.timeout_seconds", "5")
	f.Add("", "x")
	f.Add("a..b", "x")
	f.Add("api_key", "x")
	f.Add("max_turns", "[1,2")
	f.Fuzz(func(t *testing.T, key, value string) {
		home := t.TempDir()
		path := userConfigPath(home)
		seed := "model: seed\n"
		write(t, path, seed)

		err := SetUser(home, key, value)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("config disappeared: %v", readErr)
		}
		if err != nil {
			if string(data) != seed {
				t.Fatalf("failed SetUser(%q) changed the file: %q", key, data)
			}
			return
		}
		var m map[string]any
		if yamlErr := yaml.Unmarshal(data, &m); yamlErr != nil {
			t.Fatalf("SetUser(%q) left invalid YAML: %v\n%s", key, yamlErr, data)
		}
	})
}
