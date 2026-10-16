package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// SET01: the display defaults are 8 and 6.
func TestConfig_SET01_DisplayDefaults(t *testing.T) {
	d := Defaults().Display
	if d.ThinkingLines != 8 || d.ToolLines != 6 {
		t.Fatalf("display defaults = %+v, want {8 6}", d)
	}
}

// SET02: a display section loads both keys.
func TestConfig_SET02_DisplayLoad(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "display:\n  thinking_lines: 12\n  tool_lines: 3\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Display.ThinkingLines != 12 || cfg.Display.ToolLines != 3 {
		t.Fatalf("display = %+v, want {12 3}", cfg.Display)
	}
}

// SET03: one key set leaves the other at its default.
func TestConfig_SET03_DisplayPartial(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "display:\n  thinking_lines: 20\n")
	cfg, _ := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Display.ThinkingLines != 20 || cfg.Display.ToolLines != 6 {
		t.Fatalf("display = %+v, want {20 6}", cfg.Display)
	}
}

// SET04: a malformed display section warns and keeps the defaults; it never
// crashes startup.
func TestConfig_SET04_DisplayMalformed(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "display: oops\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Display.ThinkingLines != 8 || cfg.Display.ToolLines != 6 {
		t.Fatalf("display = %+v, want the defaults", cfg.Display)
	}
	if !strings.Contains(strings.ToLower(warnText(warnings)), "display") {
		t.Fatalf("no warning for a malformed display section: %s", warnText(warnings))
	}
}

// SET05: out-of-range values are clamped to 1..50 with a warning.
func TestConfig_SET05_DisplayClamp(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".config", "nova", "config.yaml"), "display:\n  thinking_lines: 999\n  tool_lines: -4\n")
	cfg, warnings := loadAt(t, cwd, home, LoadOptions{})
	if cfg.Display.ThinkingLines != 50 || cfg.Display.ToolLines != 1 {
		t.Fatalf("display = %+v, want {50 1}", cfg.Display)
	}
	if !strings.Contains(warnText(warnings), "out of range") {
		t.Fatalf("no clamp warning: %s", warnText(warnings))
	}
}

// SET06: SetUser round-trips a display key and preserves every other key.
func TestConfig_SET06_SetUserDisplay(t *testing.T) {
	home := t.TempDir()
	if err := SetUser(home, "model", "keep/me"); err != nil {
		t.Fatal(err)
	}
	if err := SetUser(home, "display.thinking_lines", 4); err != nil {
		t.Fatal(err)
	}
	if err := SetUser(home, "display.tool_lines", 9); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(LoadOptions{CWD: t.TempDir(), Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "keep/me" {
		t.Fatalf("model = %q, want it preserved", cfg.Model)
	}
	if cfg.Display.ThinkingLines != 4 || cfg.Display.ToolLines != 9 {
		t.Fatalf("display = %+v, want {4 9}", cfg.Display)
	}
}
