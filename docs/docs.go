// Package docs embeds Nova's markdown documentation so `nova docs` can print it
// without shipping the source tree.
package docs

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.md
var files embed.FS

// List returns the available topics (markdown file names without the
// extension), sorted.
func List() []string {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".md"))
	}
	sort.Strings(out)
	return out
}

// Get returns one topic's markdown. Topic names are case-insensitive.
func Get(topic string) (string, bool) {
	want := strings.ToLower(topic) + ".md"
	for _, name := range List() {
		if strings.ToLower(name)+".md" == want {
			data, err := files.ReadFile(name + ".md")
			if err != nil {
				return "", false
			}
			return string(data), true
		}
	}
	return "", false
}
