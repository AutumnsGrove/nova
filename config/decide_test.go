package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_DecideRoles(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
decide:
  allow_aliases: true
  roles:
    oracle:
      model: typesafe/jev-1.14
      fallback: typesafe/jev-1.13
      timeout_ms: 2500
`)
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if !cfg.Decide.AllowAliases {
		t.Error("allow_aliases was not read")
	}
	role, ok := cfg.Decide.Roles["oracle"]
	if !ok {
		t.Fatalf("roles = %v, want oracle", cfg.Decide.Roles)
	}
	if role.Model != "typesafe/jev-1.14" || role.Fallback != "typesafe/jev-1.13" || role.TimeoutMS != 2500 {
		t.Fatalf("role = %+v", role)
	}
}

func TestLoad_DecideWarnsOnABadRole(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
decide:
  roles:
    oracle:
      timeout_ms: -5
`)
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if got := cfg.Decide.Roles["oracle"].TimeoutMS; got != 0 {
		t.Errorf("timeout_ms = %d, want 0", got)
	}
	text := warnText(warnings)
	if !strings.Contains(text, "negative decide.roles.oracle.timeout_ms") {
		t.Errorf("warnings = %s, want a negative-timeout warning", text)
	}
	if !strings.Contains(text, `decide role "oracle" has no model`) {
		t.Errorf("warnings = %s, want a missing-model warning", text)
	}
}

func TestLoad_DecideUnknownKeyWarns(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
decide:
  roles:
    oracle:
      modle: typesafe/jev-1.14
`)
	_, warnings := loadAt(t, cwd, home, LoadOptions{})
	if got := warnText(warnings); !strings.Contains(got, "unknown key: decide.roles.modle") {
		t.Fatalf("warnings = %s, want the misspelled key named", got)
	}
}
