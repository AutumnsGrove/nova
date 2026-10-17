package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// write makes a file and its parents.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// envFrom returns an Env lookup over a fixed map.
func envFrom(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// alwaysTrusted trusts every directory.
func alwaysTrusted(string) bool { return true }

// loadAt calls Load with cwd and home filled in and fails on error.
func loadAt(t *testing.T, cwd, home string, opt LoadOptions) (*Config, []Warning) {
	t.Helper()
	opt.CWD = cwd
	opt.Home = home
	cfg, warnings, err := Load(opt)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg, warnings
}

// warnText joins the warnings for substring checks.
func warnText(warnings []Warning) string {
	var b strings.Builder
	for _, w := range warnings {
		b.WriteString(w.Source)
		b.WriteString(": ")
		b.WriteString(w.Message)
		b.WriteString("\n")
	}
	return b.String()
}

func TestLoad_CFG01_DefaultsOnly(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if !reflect.DeepEqual(*cfg, Defaults()) {
		t.Fatalf("config = %+v, want defaults %+v", *cfg, Defaults())
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

func TestLoad_CFG02_UserOverridesDefaults(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "model: a\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Model != "a" {
		t.Fatalf("model = %q, want a", cfg.Model)
	}
}

func TestLoad_CFG03_ProjectOverridesUser(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "model: user\n")
	write(t, filepath.Join(cwd, ".nova", "config.yaml"), "model: project\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{Trusted: alwaysTrusted})
	if cfg.Model != "project" {
		t.Fatalf("model = %q, want project", cfg.Model)
	}
}

func TestLoad_CFG04_ProjectSkippedWhenUntrusted(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "model: user\n")
	write(t, filepath.Join(cwd, ".nova", "config.yaml"), "model: project\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Model != "user" {
		t.Fatalf("model = %q, want user", cfg.Model)
	}
	if !strings.Contains(warnText(warnings), "trust") {
		t.Fatalf("warnings should say the project is untrusted:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG05_EnvOverridesFiles(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "model: file\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{Env: envFrom(map[string]string{"NOVA_MODEL": "env"})})
	if cfg.Model != "env" {
		t.Fatalf("model = %q, want env", cfg.Model)
	}
}

func TestLoad_CFG06_FlagsOverrideEnv(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg, _ := loadAt(t, cwd, home, LoadOptions{
		Env:   envFrom(map[string]string{"NOVA_MODEL": "env"}),
		Flags: map[string]any{"model": "flag"},
	})
	if cfg.Model != "flag" {
		t.Fatalf("model = %q, want flag", cfg.Model)
	}
}

func TestLoad_CFG07_DeepMergeMaps(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"),
		"provider:\n  openrouter:\n    routing:\n      a: 1\n")
	write(t, filepath.Join(cwd, ".nova", "config.yaml"),
		"provider:\n  openrouter:\n    routing:\n      b: 2\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{Trusted: alwaysTrusted})
	routing := cfg.Provider.OpenRouter.Routing
	if routing["a"] != 1 || routing["b"] != 2 {
		t.Fatalf("routing = %+v, want both keys\n%s", routing, warnText(warnings))
	}
}

func TestLoad_CFG08_ListsReplaced(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "tools:\n  active: [a, b]\n")
	write(t, filepath.Join(cwd, ".nova", "config.yaml"), "tools:\n  active: [c]\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{Trusted: alwaysTrusted})
	if !reflect.DeepEqual(cfg.Tools.Active, []string{"c"}) {
		t.Fatalf("active = %v, want [c]", cfg.Tools.Active)
	}
}

func TestLoad_CFG09_ExplicitFalseAndZeroOverride(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"),
		"compaction:\n  enabled: true\nmax_turns: 5\n")
	write(t, filepath.Join(cwd, ".nova", "config.yaml"),
		"compaction:\n  enabled: false\nmax_turns: 0\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{Trusted: alwaysTrusted})
	if cfg.Compaction.Enabled == nil || *cfg.Compaction.Enabled {
		t.Fatalf("enabled = %v, want false", cfg.Compaction.Enabled)
	}
	if cfg.MaxTurns != 0 {
		t.Fatalf("max_turns = %d, want 0", cfg.MaxTurns)
	}
}

func TestLoad_CFG10_MissingFiles(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{Trusted: alwaysTrusted})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if !reflect.DeepEqual(*cfg, Defaults()) {
		t.Fatalf("config = %+v, want defaults", *cfg)
	}
}

func TestLoad_CFG16_EnvBlankIgnored(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "model: a\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{Env: envFrom(map[string]string{"NOVA_MODEL": "   "})})
	if cfg.Model != "a" {
		t.Fatalf("model = %q, want a", cfg.Model)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

func TestLoad_CFG17_EnvUnparseable(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{Env: envFrom(map[string]string{"NOVA_MAX_TURNS": "abc"})})
	if cfg.MaxTurns != Defaults().MaxTurns {
		t.Fatalf("max_turns = %d, want default", cfg.MaxTurns)
	}
	if !strings.Contains(warnText(warnings), "NOVA_MAX_TURNS") {
		t.Fatalf("warnings should name the variable:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG18_UnknownFlagKey(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	_, warnings := loadAt(t, cwd, home, LoadOptions{Flags: map[string]any{"nope": 1}})
	if !strings.Contains(warnText(warnings), "nope") {
		t.Fatalf("warnings should name the unknown flag:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG25_ProjectRootDetection(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".nova", "config.yaml"), "model: root\n")
	cwd := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadAt(t, cwd, home, LoadOptions{Trusted: alwaysTrusted})
	if cfg.Model != "root" {
		t.Fatalf("model = %q, want root", cfg.Model)
	}
}

func TestLoad_CFG26_NoProjectRoot(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{Trusted: alwaysTrusted})
	if !reflect.DeepEqual(*cfg, Defaults()) || len(warnings) != 0 {
		t.Fatalf("config = %+v warnings = %v, want defaults and none", *cfg, warnings)
	}
}

func TestLoad_CFG27_MergeDeterministic(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"),
		"modle: x\nthinking: extreme\npermissions:\n  headless: ask\n")
	first, firstWarnings := loadAt(t, cwd, home, LoadOptions{})
	for i := 0; i < 100; i++ {
		cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
		if !reflect.DeepEqual(*cfg, *first) {
			t.Fatalf("run %d: config differs", i)
		}
		if !reflect.DeepEqual(warnings, firstWarnings) {
			t.Fatalf("run %d: warnings differ:\n%v\n%v", i, warnings, firstWarnings)
		}
	}
}

func TestDisplayImageDefaults(t *testing.T) {
	d := Defaults().Display
	if !d.ShowImages || d.ImageWidthCells != 60 {
		t.Fatalf("display defaults = %+v, want show_images true and 60 cells", d)
	}
}
