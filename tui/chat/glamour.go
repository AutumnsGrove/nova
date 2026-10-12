//go:build !notui

package chat

import (
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// glamourRenderer renders markdown with glamour, caching one term renderer per
// width because wrapping is fixed at construction.
type glamourRenderer struct {
	st    styles.Styles
	mu    sync.Mutex
	cache map[int]*glamour.TermRenderer
}

// NewGlamourRenderer returns the production markdown renderer. It degrades to
// plain text styling when the profile has no colour or the theme is Mono.
func NewGlamourRenderer(st styles.Styles) Renderer {
	return &glamourRenderer{st: st, cache: map[int]*glamour.TermRenderer{}}
}

// Markdown renders one block at width. A render error falls back to the raw
// source so the transcript never loses text.
func (g *glamourRenderer) Markdown(src string, width int) string {
	tr := g.renderer(width)
	if tr == nil {
		return src
	}
	out, err := tr.Render(src)
	if err != nil {
		return src
	}
	return strings.TrimRight(out, "\n")
}

// renderer returns the term renderer for a width, building it once.
func (g *glamourRenderer) renderer(width int) *glamour.TermRenderer {
	g.mu.Lock()
	defer g.mu.Unlock()
	if tr, ok := g.cache[width]; ok {
		return tr
	}
	style := "dark"
	switch {
	case g.st.Profile <= colorprofile.ASCII || g.st.Theme.Name == "mono":
		style = "notty"
	case g.st.Theme.Name == "nova-light":
		style = "light"
	}
	tr, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
	if err != nil {
		return nil
	}
	g.cache[width] = tr
	return tr
}
