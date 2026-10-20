//go:build !notui

package chat

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/sanitize"
)

// Renderer renders one markdown block at a width. The markdown type calls it
// once per closed block and caches the result, so a stream of deltas is cheap.
// NewGlamourRenderer is the production implementation; tests use a fake.
type Renderer interface {
	Markdown(src string, width int) string
}

// markdown renders text incrementally. It splits the source into blocks at
// blank lines outside code fences, renders each closed block once per width,
// and renders the open tail with a cheap wrapper. The number of Renderer
// calls is proportional to the number of blocks, not the number of deltas.
type markdown struct {
	r             Renderer
	width         int
	renderedWidth int
	pending       string   // the open tail block
	closed        []string // closed block sources
	rendered      []string // closed block renders at renderedWidth
	calls         int      // Renderer.Markdown calls, for tests and benchmarks
}

// newMarkdown returns an empty incremental renderer.
func newMarkdown(r Renderer) *markdown { return &markdown{r: r} }

// setWidth changes the width. The next String re-renders closed blocks once.
func (m *markdown) setWidth(width int) { m.width = width }

// append adds deltas to the open block and closes any block the new text
// completed.
func (m *markdown) append(text string) {
	if text == "" {
		return
	}
	m.pending += text
	m.drain()
}

// drain moves every complete block from pending into closed.
func (m *markdown) drain() {
	for {
		block, rest, ok := splitBlock(m.pending)
		if !ok {
			return
		}
		m.pending = rest
		if strings.TrimSpace(block) != "" {
			// Model and tool output must never carry ANSI into the UI.
			m.closed = append(m.closed, sanitize.Text(block))
		}
	}
}

// String renders the closed blocks and the open tail. It is safe to call at
// any time, including before any text arrives.
func (m *markdown) String() string {
	if m.width != m.renderedWidth {
		m.rendered = m.rendered[:0]
		m.renderedWidth = m.width
	}
	for len(m.rendered) < len(m.closed) {
		m.rendered = append(m.rendered, m.render(m.closed[len(m.rendered)]))
	}
	parts := append([]string(nil), m.rendered...)
	if tail := strings.TrimRight(m.pending, "\n"); strings.TrimSpace(tail) != "" {
		parts = append(parts, cheap(tail, m.width))
	}
	// Glamour's document style prefixes and suffixes every block with a blank,
	// margin-only line. Trim those edges so the join below leaves exactly one
	// blank line between blocks instead of stacking them.
	shown := parts[:0]
	for _, p := range parts {
		if p = trimBlankEdges(p); p != "" {
			shown = append(shown, p)
		}
	}
	return strings.Join(shown, "\n\n")
}

// trimBlankEdges drops leading and trailing lines that hold no visible text
// (whitespace and ANSI only), leaving inner blank lines untouched.
func trimBlankEdges(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(sanitize.Text(lines[0])) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(sanitize.Text(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// render renders one closed block through the Renderer.
func (m *markdown) render(block string) string {
	m.calls++
	if m.r == nil {
		return cheap(block, m.width)
	}
	return m.r.Markdown(block, m.width)
}

// cheap wraps text to width without the full markdown renderer. It strips ANSI
// first so output cannot move the cursor or change colours.
func cheap(src string, width int) string {
	src = sanitize.Text(src)
	if width <= 0 {
		return src
	}
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		lines[i] = wrap(expandTabs(line, 4), width)
	}
	return strings.Join(lines, "\n")
}

// wrap word-wraps a line, hard-wrapping words longer than the width.
func wrap(line string, width int) string {
	if width <= 0 || ansi.StringWidth(line) <= width {
		return line
	}
	wrapped := strings.Split(ansi.Wordwrap(line, width, ""), "\n")
	var out []string
	for _, l := range wrapped {
		if ansi.StringWidth(l) > width {
			l = strings.TrimRight(ansi.Hardwrap(l, width, false), "\n")
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// expandTabs replaces tabs with spaces to the next multiple of size.
func expandTabs(line string, size int) string {
	var b strings.Builder
	col := 0
	for _, r := range line {
		if r == '\t' {
			n := size - col%size
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += ansi.StringWidth(string(r))
	}
	return b.String()
}

// splitBlock finds the first block boundary in s: a complete blank line
// outside a code fence. It returns the block before it and the remainder
// after any following complete blank lines. A line without a trailing newline
// is incomplete: more text may still arrive, so it never ends a block. ok is
// false when s holds only an open block.
func splitBlock(s string) (block, rest string, ok bool) {
	fence := ""
	i := 0
	for {
		j := strings.IndexByte(s[i:], '\n')
		if j < 0 {
			return "", s, false
		}
		line := s[i : i+j]
		next := i + j + 1
		trimmed := strings.TrimSpace(line)
		switch {
		case fence != "":
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
		case fenceMarker(trimmed) != "":
			fence = fenceMarker(trimmed)
		case trimmed == "":
			block = strings.TrimRight(s[:i], "\n")
			k := next
			for k < len(s) {
				j2 := strings.IndexByte(s[k:], '\n')
				if j2 < 0 || strings.TrimSpace(s[k:k+j2]) != "" {
					break
				}
				k += j2 + 1
			}
			return block, s[k:], true
		}
		i = next
	}
}

// fenceMarker returns the fence marker a trimmed line opens, or "".
func fenceMarker(trimmed string) string {
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, marker) {
			return marker
		}
	}
	return ""
}
