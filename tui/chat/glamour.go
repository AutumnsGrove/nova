//go:build !notui

package chat

import (
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/charmbracelet/colorprofile"

	novastyles "github.com/AutumnsGrove/nova/tui/styles"
)

// glamourRenderer renders markdown with glamour, caching one term renderer per
// width because wrapping is fixed at construction.
type glamourRenderer struct {
	st    novastyles.Styles
	mu    sync.Mutex
	cache map[int]*glamour.TermRenderer
}

// NewGlamourRenderer returns the production markdown renderer. It degrades to
// plain text styling when the profile has no colour or the theme is Mono.
func NewGlamourRenderer(st novastyles.Styles) Renderer {
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
	cfg := baseStyle(g.st)
	// The transcript already spaces blocks, and it renders the open tail
	// without glamour. Drop glamour's document padding (a two-space margin and
	// blank lines around it) so a closed block is flush and does not jump when
	// the streaming tail becomes one.
	cfg.Document.Margin = nil
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	tr, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(width))
	if err != nil {
		return nil
	}
	g.cache[width] = tr
	return tr
}

// baseStyle picks the glamour palette for the theme and profile.
func baseStyle(st novastyles.Styles) ansi.StyleConfig {
	switch {
	case st.Profile <= colorprofile.ASCII || st.Theme.Name == "mono":
		return styles.NoTTYStyleConfig
	case st.Theme.Name == "nova-light":
		return styles.LightStyleConfig
	default:
		return styles.DarkStyleConfig
	}
}
