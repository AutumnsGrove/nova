package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoad_CFG11_InvalidYAML(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	path := filepath.Join(home, ".config", "nova", "config.yaml")
	write(t, path, "a: [\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if !reflect.DeepEqual(*cfg, Defaults()) {
		t.Fatalf("config = %+v, want defaults", *cfg)
	}
	text := warnText(warnings)
	if !strings.Contains(text, path) || !strings.Contains(text, "line") {
		t.Fatalf("warning should name the file and line:\n%s", text)
	}
}

func TestLoad_CFG12_UnreadableFile(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	path := filepath.Join(home, ".config", "nova", "config.yaml")
	opt := LoadOptions{ReadFile: func(p string) ([]byte, error) {
		if p == path {
			return nil, &os.PathError{Op: "open", Path: p, Err: os.ErrPermission}
		}
		return nil, os.ErrNotExist
	}}
	cfg, warnings := loadAt(t, cwd, home, opt)
	if !reflect.DeepEqual(*cfg, Defaults()) {
		t.Fatalf("config = %+v, want defaults", *cfg)
	}
	if !strings.Contains(warnText(warnings), "cannot read") {
		t.Fatalf("warning should report the read failure:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG13_UnknownKey(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "modle: x\n")
	_, warnings := loadAt(t, cwd, home, LoadOptions{})
	if !strings.Contains(warnText(warnings), "modle") {
		t.Fatalf("warning should name the unknown key:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG14_UnknownNestedKey(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "tools:\n  bash:\n    nope: 1\n")
	_, warnings := loadAt(t, cwd, home, LoadOptions{})
	if !strings.Contains(warnText(warnings), "tools.bash.nope") {
		t.Fatalf("warning should use the dotted path:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG15_WrongType(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "max_turns: \"ten\"\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if cfg.MaxTurns != Defaults().MaxTurns {
		t.Fatalf("max_turns = %d, want default", cfg.MaxTurns)
	}
	if len(warnings) == 0 {
		t.Fatal("wrong type should warn")
	}
}

func TestLoad_CFG19_TildeExpansion(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "extensions:\n  dirs: [\"~/x\"]\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{})
	if !reflect.DeepEqual(cfg.Extensions.Dirs, []string{filepath.Join(home, "x")}) {
		t.Fatalf("dirs = %v, want [%s]", cfg.Extensions.Dirs, filepath.Join(home, "x"))
	}
}

func TestLoad_CFG20_InvalidThinking(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "thinking: extreme\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Thinking != "" {
		t.Fatalf("thinking = %q, want empty", cfg.Thinking)
	}
	if !strings.Contains(warnText(warnings), "thinking") {
		t.Fatalf("warning should name thinking:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG21_NegativeNumbers(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "max_turns: -5\nretry:\n  max: -1\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if cfg.MaxTurns != 0 || cfg.Retry.Max != 0 {
		t.Fatalf("max_turns = %d retry.max = %d, want 0", cfg.MaxTurns, cfg.Retry.Max)
	}
	if !strings.Contains(warnText(warnings), "negative") {
		t.Fatalf("warning should mention a negative number:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG22_InvalidPermissions(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "permissions:\n  headless: ask\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if !reflect.DeepEqual(cfg.Permissions, Defaults().Permissions) {
		t.Fatalf("permissions = %+v, want the default policy", cfg.Permissions)
	}
	if !strings.Contains(warnText(warnings), "permissions") {
		t.Fatalf("warning should mention permissions:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG23_JSONAccepted(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.json"), `{"model": "json"}`)
	cfg, _ := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Model != "json" {
		t.Fatalf("model = %q, want json", cfg.Model)
	}
}

func TestLoad_CFG24_YAMLWinsOverJSON(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "model: yaml\n")
	write(t, filepath.Join(home, ".config", "nova", "config.json"), `{"model": "json"}`)
	cfg, _ := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Model != "yaml" {
		t.Fatalf("model = %q, want yaml", cfg.Model)
	}
}

func TestLoad_CFG28_VeryLargeConfig(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	body := strings.Repeat("x", 5<<20)
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "system_prompt: \""+body+"\"\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{})
	if cfg == nil {
		t.Fatal("config is nil")
	}
}

func TestLoad_CFG29_AnchorsAndAliases(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "model: &m base\ntheme: *m\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Model != "base" || cfg.Theme != "base" {
		t.Fatalf("model = %q theme = %q, want both base", cfg.Model, cfg.Theme)
	}

	laughs := "provider:\n  openrouter:\n    routing:\n" +
		"      a: &a [x,x,x,x,x,x,x,x,x,x]\n" +
		"      b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a,*a]\n" +
		"      c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b,*b]\n" +
		"      d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c,*c]\n" +
		"      e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d,*d]\n" +
		"      f: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e,*e]\n" +
		"      g: &g [*f,*f,*f,*f,*f,*f,*f,*f,*f,*f]\n" +
		"      h: [*g,*g,*g,*g,*g,*g,*g,*g,*g,*g]\n"
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), laughs)
	_, warnings := loadAt(t, cwd, home, LoadOptions{})
	if !strings.Contains(warnText(warnings), "alias") {
		t.Fatalf("billion laughs should be rejected for excessive aliasing:\n%s", warnText(warnings))
	}
}

func TestLoad_CFG30_DuplicateKeys(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "model: a\nmodel: b\n")
	_, warnings := loadAt(t, cwd, home, LoadOptions{})
	if len(warnings) == 0 {
		t.Fatal("duplicate keys should warn")
	}
}
