package main

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/docs"
	"github.com/AutumnsGrove/nova/examples"
	"github.com/AutumnsGrove/nova/ext/sdk"
)

// docTopic is one thing `nova docs` can print: the embedded docs, the SDK
// documentation, an example index, or one example.
type docTopic struct {
	name  string
	title string
	body  string
}

// docTopics returns every topic, sorted by name.
func docTopics() []docTopic {
	var out []docTopic
	for _, name := range docs.List() {
		name = strings.ToLower(name)
		if body, ok := docs.Get(name); ok {
			out = append(out, docTopic{name: name, title: name, body: body})
		}
	}
	out = append(out, docTopic{name: "sdk", title: "extension sdk", body: sdk.Markdown()})
	out = append(out, exampleTopics()...)
	out = append(out, docTopic{name: "examples", title: "example extensions", body: examplesIndex()})
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// exampleTopics returns one topic per example directory, holding its README,
// manifest, and Go source so the model can copy the whole shape.
func exampleTopics() []docTopic {
	entries, err := examples.Files.ReadDir("extensions")
	if err != nil {
		return nil
	}
	var out []docTopic
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		body := readExample(name)
		if body == "" {
			continue
		}
		out = append(out, docTopic{name: "example-" + name, title: "example " + name, body: body})
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
		first := firstLine(t.body)
		fmt.Fprintf(&b, "\n- %s: %s", strings.TrimPrefix(t.name, "example-"), first)
	}
	b.WriteString("\n")
	return b.String()
}

// firstLine returns the first non-empty, non-heading line of text.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "##") {
			continue
		}
		return line
	}
	return ""
}

// docResult is one search hit.
type docResult struct {
	Name    string
	Excerpt string
	Score   int
}

// searchDocs ranks topics by term frequency: a title match weighs five times a
// body match. It returns at most five results, best first.
func searchDocs(topics []docTopic, terms []string) []docResult {
	var results []docResult
	for _, t := range topics {
		score := 0
		for _, term := range terms {
			lower := strings.ToLower(term)
			if lower == "" {
				continue
			}
			score += 5 * strings.Count(strings.ToLower(t.name), lower)
			score += strings.Count(strings.ToLower(t.body), lower)
		}
		if score == 0 {
			continue
		}
		results = append(results, docResult{Name: t.name, Excerpt: excerpt(t.body, terms), Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Name < results[j].Name
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
				if len(trimmed) > 100 {
					return trimmed[:100]
				}
				return trimmed
			}
		}
	}
	return ""
}

// authoringContext is the whole context `nova docs` can give the model to write
// its own extension: the extension and protocol docs, the SDK, and every example.
func authoringContext() string {
	var b strings.Builder
	for _, name := range []string{"extensions", "protocol"} {
		if body, ok := docs.Get(name); ok {
			b.WriteString(body)
			b.WriteString("\n")
		}
	}
	b.WriteString(sdk.Markdown())
	b.WriteString("\n")
	for _, t := range exampleTopics() {
		b.WriteString(t.body)
		b.WriteString("\n")
	}
	return b.String()
}

// docsCommand lists, prints, and searches the embedded documentation.
func docsCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "docs [topic|search]",
		Short: "Print embedded documentation",
		Long:  "Print an embedded document. With no topic, list the topics; with `search <terms>`, rank topics by term frequency.",
		RunE: func(_ *cobra.Command, args []string) error {
			topics := docTopics()
			switch {
			case len(args) == 0:
				for _, t := range topics {
					fmt.Fprintln(stdout, t.name)
				}
				return nil
			case args[0] == "search":
				results := searchDocs(topics, args[1:])
				if len(results) == 0 {
					fmt.Fprintln(stdout, "no matching topics")
					return nil
				}
				for _, r := range results {
					fmt.Fprintf(stdout, "%s\t%s\n", r.Name, r.Excerpt)
				}
				return nil
			}
			name := args[0]
			for _, t := range topics {
				if t.name == name {
					fmt.Fprint(stdout, t.body)
					if !strings.HasSuffix(t.body, "\n") {
						fmt.Fprintln(stdout)
					}
					return nil
				}
			}
			fmt.Fprintf(stdout, "unknown topic %q; topics: %s\n", name, strings.Join(topicNames(topics), ", "))
			return nil
		},
	}
}

// topicNames returns the topic names in order.
func topicNames(topics []docTopic) []string {
	out := make([]string, len(topics))
	for i, t := range topics {
		out[i] = t.name
	}
	return out
}
