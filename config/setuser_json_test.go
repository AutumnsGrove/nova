package config

import (
	"path/filepath"
	"testing"
)

// TestSetUser_CFW11_JSONOnlyConfigIsKept checks that editing a user config that
// exists only as config.json keeps its settings. Load prefers config.yaml, so a
// fresh YAML file written beside the JSON would otherwise hide every setting in
// it.
func TestSetUser_CFW11_JSONOnlyConfigIsKept(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.json"), `{"model": "json-model", "theme": "dark"}`)
	if err := SetUser(home, "display.thinking_lines", 12); err != nil {
		t.Fatalf("SetUser: %v", err)
	}
	cfg, _ := loadAt(t, t.TempDir(), home, LoadOptions{})
	if cfg.Display.ThinkingLines != 12 {
		t.Errorf("thinking_lines = %d, want the edited value 12", cfg.Display.ThinkingLines)
	}
	if cfg.Model != "json-model" || cfg.Theme != "dark" {
		t.Errorf("settings from config.json were lost: model=%q theme=%q", cfg.Model, cfg.Theme)
	}
}
