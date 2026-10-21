package docs

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/examples"
	"github.com/AutumnsGrove/nova/ext/sdk"
)

// Topic is one thing `nova docs` can print: the embedded docs, the SDK
// documentation, an example index, or one example.
type Topic struct {
	Name  string
	Title string
	Body  string
}

// Topics returns every topic, sorted by name.
func Topics() []Topic {
	var out []Topic
	for _, name := range List() {
		name = strings.ToLower(name)
		if body, ok := Get(name); ok {
			out = append(out, Topic{Name: name, Title: name, Body: body})
		}
	}
	out = append(out, Topic{Name: "sdk", Title: "extension sdk", Body: sdk.Markdown()})
	out = append(out, exampleTopics()...)
	out = append(out, Topic{Name: "examples", Title: "example extensions", Body: examplesIndex()})
	slices.SortFunc(out, func(a, b Topic) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// exampleTopics returns one topic per example directory, holding its README,
// manifest, and Go source so the model can copy the whole shape.
func exampleTopics() []Topic {
	entries, err := examples.Files.ReadDir("extensions")
	if err != nil {
		return nil
	}
	var out []Topic
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		body := readExample(name)
		if body == "" {
			continue
		}
		out = append(out, Topic{Name: "example-" + name, Title: "example " + name, Body: body})
	}
	return out
}

// readExample concatenates an example's README, manifest, and Go source.
func readExample(name string) string {
	var b strings.Builder
	for _, file := range []string{"README.md", "extension.yaml", "main.go"} {
		p := path.Join("extensions", name, file)
		if data, err := examples.Files.ReadFile(p); err == nil {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "## %s/%s\n\n", name, file)
			b.Write(data)
			if !strings.HasSuffix(string(data), "\n") {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// examplesIndex is the one-line-per-example overview.
func examplesIndex() string {
	var b strings.Builder
	b.WriteString("# Example extensions\n\nEvery example passes `nova ext check`. Read one with `nova docs example-<name>`.\n")
	for _, t := range exampleTopics() {
		first := firstLine(t.Body)
		fmt.Fprintf(&b, "\n- %s: %s", strings.TrimPrefix(t.Name, "example-"), first)
	}
	b.WriteString("\n")
	return b.String()
}

// firstLine returns the first non-empty, non-heading line of text.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

// Result is one search hit.
type Result struct {
	Name    string
	Excerpt string
	Score   int
}

// Search ranks topics by term frequency: a title match weighs five times a
// body match. It returns at most five results, best first.
func Search(topics []Topic, terms []string) []Result {
	var results []Result
	for _, t := range topics {
		score := 0
		for _, term := range terms {
			lower := strings.ToLower(term)
			if lower == "" {
				continue
			}
			score += 5 * strings.Count(strings.ToLower(t.Name), lower)
			score += strings.Count(strings.ToLower(t.Body), lower)
		}
		if score == 0 {
			continue
		}
		results = append(results, Result{Name: t.Name, Excerpt: excerpt(t.Body, terms), Score: score})
	}
	slices.SortFunc(results, func(a, b Result) int {
		return cmp.Or(b.Score-a.Score, strings.Compare(a.Name, b.Name))
	})
	if len(results) > 5 {
		results = results[:5]
	}
	return results
}

// excerpt returns the first line of body containing one of the terms, trimmed.
func excerpt(body string, terms []string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lower := strings.ToLower(trimmed)
		for _, term := range terms {
			if term != "" && strings.Contains(lower, strings.ToLower(term)) {
				return truncateRunes(trimmed, 100)
			}
		}
	}
	return ""
}

// truncateRunes cuts s to at most n bytes without splitting a character.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// AuthoringContext is the whole context `nova docs` can give the model to write
// its own extension: the extension and protocol docs, the SDK, and every example.
func AuthoringContext() string {
	var b strings.Builder
	for _, name := range []string{"extensions", "protocol"} {
		if body, ok := Get(name); ok {
			b.WriteString(body)
			b.WriteString("\n")
		}
	}
	b.WriteString(sdk.Markdown())
	b.WriteString("\n")
	for _, t := range exampleTopics() {
		b.WriteString(t.Body)
		b.WriteString("\n")
	}
	return b.String()
}
