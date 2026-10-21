//go:build !notui

// Package trust is the pre-start prompt that asks whether a project folder may
// contribute config, extensions, hooks, and context files. It only reads
// directory entries and config keys; it never loads or runs an extension.
package trust

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/tui/sanitize"
)

// Decision is the user's answer to the trust prompt.
type Decision int

// The three choices. Trust records the folder; Once loads nothing; Quit stops.
const (
	Trust Decision = iota
	Once
	Quit
)

// Found is what Discover would load from a project.
type Found struct {
	Config       bool
	Extensions   []string
	Hooks        int
	ContextFiles []string
}

// Empty reports whether the project holds nothing worth trusting.
func (f Found) Empty() bool {
	return !f.Config && len(f.Extensions) == 0 && f.Hooks == 0 && len(f.ContextFiles) == 0
}

// ErrTooBroad is shown when a folder is too broad to trust.
var ErrTooBroad = errors.New("that folder is too broad to trust")

// Discover lists what the folder (and its project root) would load. It reads
// only names and the config key it needs, never executing anything.
func Discover(dir string) Found {
	roots := []string{dir}
	if root, ok := config.ProjectRoot(dir); ok && root != dir {
		roots = append(roots, root)
	}
	var f Found
	for _, root := range roots {
		nova := config.ProjectConfigDir(root)
		if fileExists(filepath.Join(nova, "config.yaml")) || fileExists(filepath.Join(nova, "config.json")) {
			f.Config = true
		}
		f.Hooks = max(f.Hooks, countHooks(filepath.Join(nova, "config.yaml")))
		f.Extensions = append(f.Extensions, extensionNames(nova)...)
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			if p := filepath.Join(root, name); fileExists(p) {
				f.ContextFiles = append(f.ContextFiles, p)
			}
		}
	}
	return f
}

// countHooks reads the yaml config just far enough to count hook entries.
func countHooks(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var cfg struct {
		Hooks []any `yaml:"hooks"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return 0
	}
	return len(cfg.Hooks)
}

// extensionNames lists the immediate extension directories below nova, plus
// nova itself when it is an extension.
func extensionNames(nova string) []string {
	var out []string
	if fileExists(filepath.Join(nova, "extension.yaml")) {
		out = append(out, filepath.Base(nova))
	}
	entries, err := os.ReadDir(filepath.Join(nova, "extensions"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() && fileExists(filepath.Join(nova, "extensions", e.Name(), "extension.yaml")) {
			out = append(out, e.Name())
		}
	}
	return out
}

// Prompt shows what would load and reads one choice from in.
func Prompt(dir string, f Found, in io.Reader, out io.Writer) (Decision, error) {
	// Every name below comes from the folder being judged, so none is drawn raw.
	fmt.Fprintf(out, "The folder %s is not trusted.\n\n", sanitize.Line(dir))
	if f.Config {
		fmt.Fprintln(out, "  config:        .nova/config.yaml")
	}
	if len(f.Extensions) > 0 {
		fmt.Fprintf(out, "  extensions:    %s\n", joinLines(f.Extensions))
	}
	if f.Hooks > 0 {
		fmt.Fprintf(out, "  hooks:         %d\n", f.Hooks)
	}
	if len(f.ContextFiles) > 0 {
		fmt.Fprintf(out, "  context files: %s\n", joinLines(f.ContextFiles))
	}
	fmt.Fprintln(out, "\n  1) Trust this folder")
	fmt.Fprintln(out, "  2) Continue without trusting (once)")
	fmt.Fprintln(out, "  3) Quit")
	scanner := bufio.NewScanner(in)
	for {
		fmt.Fprint(out, "\nChoice [1]: ")
		if !scanner.Scan() {
			return Quit, nil
		}
		switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
		case "", "1", "y", "yes", "t", "trust":
			return Trust, nil
		case "2", "n", "no", "once", "c":
			return Once, nil
		case "3", "q", "quit":
			return Quit, nil
		}
	}
}

// joinLines cleans each name and joins them for one line of the prompt.
func joinLines(names []string) string {
	clean := make([]string, len(names))
	for i, n := range names {
		clean[i] = sanitize.Line(n)
	}
	return strings.Join(clean, ", ")
}

// ApplyTrust records the folder, translating a too-broad refusal.
func ApplyTrust(dir string, store config.TrustStore) error {
	if err := store.Trust(dir); err != nil {
		if errors.Is(err, config.ErrTooBroad) {
			return ErrTooBroad
		}
		return err
	}
	return nil
}

// Ensure runs the whole pre-start flow. An already trusted folder or an empty
// project needs no prompt; a non-interactive run stays untrusted.
func Ensure(dir string, store config.TrustStore, f Found, interactive bool, in io.Reader, out io.Writer) (Decision, error) {
	if store.IsTrusted(dir) || f.Empty() {
		return Trust, nil
	}
	if !interactive {
		return Once, nil
	}
	d, err := Prompt(dir, f, in, out)
	if err != nil {
		return d, err
	}
	if d == Trust {
		if err := ApplyTrust(dir, store); err != nil {
			fmt.Fprintln(out, err)
			return Once, nil
		}
	}
	return d, nil
}

// fileExists reports whether path is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
