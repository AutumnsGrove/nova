package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_EmbedRoles(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
embed:
  allow_aliases: true
  roles:
    index:
      model: perplexity/pplx-embed-v1-0.6b
      fallback: perplexity/pplx-embed-v1-4b
      timeout_ms: 5000
      dims: 1024
`)
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if !cfg.Embed.AllowAliases {
		t.Error("allow_aliases was not read")
	}
	role, ok := cfg.Embed.Roles["index"]
	if !ok {
		t.Fatalf("roles = %v, want index", cfg.Embed.Roles)
	}
	if role.Model != "perplexity/pplx-embed-v1-0.6b" || role.Fallback != "perplexity/pplx-embed-v1-4b" || role.TimeoutMS != 5000 || role.Dims != 1024 {
		t.Fatalf("role = %+v", role)
	}
}

func TestLoad_EmbedWarnsOnABadRole(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), `
embed:
  roles:
    index:
      timeout_ms: -5
      dims: -1
`)
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	role := cfg.Embed.Roles["index"]
	if role.TimeoutMS != 0 || role.Dims != 0 {
		t.Errorf("role = %+v, want negative values clamped to 0", role)
	}
	text := warnText(warnings)
	for _, want := range []string{"negative embed.roles.index.timeout_ms", "negative embed.roles.index.dims", `embed role "index" has no model`} {
		if !strings.Contains(text, want) {
			t.Errorf("warnings = %s, want %q", text, want)
		}
	}
}

func TestLoad_EmbedMaxPerRun(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "extensions:\n  embed_max_per_run: -1\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if got := cfg.Extensions.EmbedMaxPerRun; got != 0 {
		t.Errorf("embed_max_per_run = %d, want 0", got)
	}
	if got := warnText(warnings); !strings.Contains(got, "extensions.embed_max_per_run") {
		t.Errorf("warnings = %s, want a negative-cap warning", got)
	}
}

func TestDefaults_EmbedMaxPerRun(t *testing.T) {
	if got := Defaults().Extensions.EmbedMaxPerRun; got != 20 {
		t.Errorf("default embed_max_per_run = %d, want 20", got)
	}
}
