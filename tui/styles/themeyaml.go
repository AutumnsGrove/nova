//go:build !notui

package styles

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// colourKeys are the palette keys a theme file may set.
var colourKeys = []string{"spark", "ink", "core", "warm", "bg", "muted", "success", "error", "panel", "panelok", "panelerr"}

// colourRe matches #rgb and #rrggbb.
var colourRe = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// themeFile is the YAML shape of a theme.
type themeFile struct {
	Name   string            `yaml:"name"`
	Colors map[string]string `yaml:"colors"`
}

// LoadTheme reads a theme YAML file. Missing keys fall back to the dark theme
// with a warning; unknown keys and invalid colours warn and fall back; a file
// that is not YAML or has no name is an error.
func LoadTheme(path string) (Theme, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Theme{}, nil, err
	}
	var raw themeFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Theme{}, nil, fmt.Errorf("theme %s: %w", path, err)
	}
	if strings.TrimSpace(raw.Name) == "" {
		return Theme{}, nil, fmt.Errorf("theme %s: missing name", path)
	}
	known := map[string]bool{}
	for _, k := range colourKeys {
		known[k] = true
	}
	t := Theme{Name: raw.Name, Colors: map[string]string{}}
	var warnings []string
	keys := make([]string, 0, len(raw.Colors))
	for k := range raw.Colors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := raw.Colors[k]
		switch {
		case !known[k]:
			warnings = append(warnings, fmt.Sprintf("%s: unknown colour key %q", raw.Name, k))
		case !colourRe.MatchString(v):
			warnings = append(warnings, fmt.Sprintf("%s: invalid colour for %q: %q", raw.Name, k, v))
		default:
			t.Colors[k] = v
		}
	}
	dark := Dark().Colors
	for _, k := range colourKeys {
		if _, ok := t.Colors[k]; !ok {
			warnings = append(warnings, fmt.Sprintf("%s: missing colour %q; using the dark default", raw.Name, k))
			t.Colors[k] = dark[k]
		}
	}
	return t, warnings, nil
}

// LoadThemes loads every *.yaml in the given directories, built-ins first. A
// user theme with a built-in's name replaces it. Unreadable or invalid files
// are skipped.
func LoadThemes(dirs []string) []Theme {
	themes := []Theme{Dark(), Light(), Mono()}
	index := map[string]int{}
	for i, t := range themes {
		index[t.Name] = i
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			t, _, err := LoadTheme(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			if i, ok := index[t.Name]; ok {
				themes[i] = t
			} else {
				index[t.Name] = len(themes)
				themes = append(themes, t)
			}
		}
	}
	return themes
}
