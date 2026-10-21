// The adapter seam: everything a real provider has to supply lives in this file.
// main.go only knows the Backend interface.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Result is one hit. It is deliberately tiny: the model reads titles, URLs, and
// snippets, then fetches a page it wants with web_fetch.
type Result struct {
	Title, URL, Snippet string
}

// Backend is the whole provider surface. Implement it for Parallel, Tavily,
// Brave, or anything else; the tool and its rendering do not change.
type Backend interface {
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

const (
	defaultLimit = 5
	maxLimit     = 10 // the model chooses limit, so bound it here
)

// canned is the example backend: deterministic results, no network, no key. It
// echoes the query so you can see the argument plumbing work.
type canned struct{}

func (canned) Search(_ context.Context, query string, _ int) ([]Result, error) {
	return []Result{
		{
			Title:   "Example result for " + query,
			URL:     "https://example.com/search?q=" + url.QueryEscape(query),
			Snippet: "A canned hit. Replace the canned backend with a real provider.",
		},
		{
			Title:   "Second example result",
			URL:     "https://example.org/second",
			Snippet: "Returned by the example, not by a search engine.",
		},
	}, nil
}

// search runs the backend and renders its hits. Every field is untrusted data,
// so it is flattened onto one line and never interpreted as instructions.
func search(ctx context.Context, b Backend, query string, limit int) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("query is required")
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	hits, err := b.Search(ctx, query, limit)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "no results", nil
	}
	hits = hits[:min(len(hits), limit)]
	var out strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&out, "%d. %s — %s\n   %s\n", i+1, oneLine(h.Title), oneLine(h.URL), oneLine(h.Snippet))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// oneLine collapses whitespace so a hostile snippet cannot fake a new result.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
