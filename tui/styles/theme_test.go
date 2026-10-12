//go:build !notui

package styles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTheme(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheme_THM01_Valid(t *testing.T) {
	path := writeTheme(t, "t.yaml", "name: my-theme\ncolors:\n  spark: \"#112233\"\n  ink: \"#fff\"\n")
	th, warnings, err := LoadTheme(path)
	if err != nil {
		t.Fatal(err)
	}
	if th.Name != "my-theme" || th.Colors["spark"] != "#112233" {
		t.Fatalf("theme = %+v", th)
	}
	for _, w := range warnings {
		if strings.Contains(w, "spark") || strings.Contains(w, "ink") {
			t.Fatalf("unexpected warning: %q", w)
		}
	}
}

func TestTheme_THM02_MissingKeys(t *testing.T) {
	path := writeTheme(t, "t.yaml", "name: partial\ncolors:\n  spark: \"#112233\"\n")
	th, warnings, err := LoadTheme(path)
	if err != nil {
		t.Fatal(err)
	}
	if th.Colors["ink"] != Dark().Colors["ink"] {
		t.Fatalf("ink did not fall back: %q", th.Colors["ink"])
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "missing") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no missing-key warning: %v", warnings)
	}
}

func TestTheme_THM03_InvalidColour(t *testing.T) {
	path := writeTheme(t, "t.yaml", "name: bad\ncolors:\n  spark: blue\n  ink: \"#12\"\n")
	th, warnings, err := LoadTheme(path)
	if err != nil {
		t.Fatal(err)
	}
	if th.Colors["spark"] != Dark().Colors["spark"] {
		t.Fatalf("spark did not fall back: %q", th.Colors["spark"])
	}
	if len(warnings) < 2 {
		t.Fatalf("expected warnings, got %v", warnings)
	}
}

func TestTheme_THM04_UnknownKey(t *testing.T) {
	path := writeTheme(t, "t.yaml", "name: extra\ncolors:\n  sparkle: \"#112233\"\n")
	_, warnings, err := LoadTheme(path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unknown-key warning: %v", warnings)
	}
}

func TestTheme_THM05_Errors(t *testing.T) {
	notYAML := writeTheme(t, "t.yaml", ": not : yaml : [")
	if _, _, err := LoadTheme(notYAML); err == nil {
		t.Fatal("expected an error for invalid YAML")
	}
	noName := writeTheme(t, "n.yaml", "colors:\n  spark: \"#112233\"\n")
	if _, _, err := LoadTheme(noName); err == nil {
		t.Fatal("expected an error for a missing name")
	}
}

func TestTheme_THM06_Precedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "user.yaml"),
		[]byte("name: nova-dark\ncolors:\n  spark: \"#abcdef\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	themes := LoadThemes([]string{dir})
	if len(themes) != 3 {
		t.Fatalf("themes = %d, want 3", len(themes))
	}
	if themes[0].Name != "nova-dark" || themes[0].Colors["spark"] != "#abcdef" {
		t.Fatalf("built-in not replaced: %+v", themes[0])
	}
	if themes[1].Name != "nova-light" || themes[2].Name != "mono" {
		t.Fatalf("built-ins out of order: %+v", themes)
	}
}
