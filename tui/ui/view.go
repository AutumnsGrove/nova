//go:build !notui

package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/splash"
)

// View renders the whole UI. It is pure: it reads model state and returns a
// view with the alt screen and mouse wheel enabled. A panic is recovered and
// recorded so Run can log it after the program exits.
func (m *Model) View() (v tea.View) {
	defer func() {
		if r := recover(); r != nil {
			m.panicValue = r
			v = tea.NewView("nova: recovered from a UI panic")
		}
	}()
	v = tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// render builds the region stack and normalises it to exactly the screen size.
func (m *Model) render() string {
	var lines []string
	if m.layout.Header.H > 0 {
		lines = append(lines, m.header())
	}
	lines = append(lines, fitLines(strings.Split(m.mainView(), "\n"), m.layout.Main.H)...)
	lines = append(lines, fitLines(strings.Split(m.editorView(), "\n"), m.layout.Editor.H)...)
	if m.layout.Footer.H > 0 {
		lines = append(lines, m.footer())
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = padLine(lines[i], m.width)
	}
	return strings.Join(lines, "\n")
}

// header is the one-line session banner.
func (m *Model) header() string {
	parts := []string{m.st.Spark.Render("✦"), m.st.Header.Render("nova")}
	if m.state.Model != "" {
		parts = append(parts, m.st.Text.Render(m.state.Model))
	}
	parts = append(parts, m.st.Muted.Render(m.shortCWD()))
	return truncateJoin(parts, m.width, "  ")
}

// mainView is the transcript, the splash, or the completion popup drawn over
// the transcript's bottom.
func (m *Model) mainView() string {
	if m.showSplash() {
		if out, _, ok := splash.Render(m.layout.Main.W, m.layout.Main.H, m.st, m.profile, m.version); ok {
			return out
		}
	}
	view := m.list.View()
	if !m.completion.active {
		return view
	}
	rows := m.completionView(m.layout.Main.W)
	if len(rows) == 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	for i, row := range rows {
		idx := len(lines) - len(rows) + i
		if idx >= 0 {
			lines[idx] = row
		}
	}
	return strings.Join(lines, "\n")
}

// editorView is the input area.
func (m *Model) editorView() string {
	return m.editor.View()
}

// footer is the one-line status bar.
func (m *Model) footer() string {
	var segs []string
	if m.state.EffectiveLimit > 0 {
		pct := float64(m.state.ContextTokens) / float64(m.state.EffectiveLimit) * 100
		segs = append(segs, contextBar(pct)+fmt.Sprintf(" %.0f%%", pct))
	} else {
		segs = append(segs, "ctx n/a")
	}
	segs = append(segs, fmt.Sprintf("$%.3f", m.state.Usage.CostUSD))
	if q := m.queued(); q > 0 {
		segs = append(segs, fmt.Sprintf("%d queued", q))
	}
	segs = append(segs, m.hint())
	return m.st.Footer.Render(truncateJoin(segs, m.width, " · "))
}

// hint is the most relevant key for the current state.
func (m *Model) hint() string {
	switch {
	case m.runActive:
		return "esc abort"
	case m.expanded:
		return "ctrl+o collapse"
	default:
		return "ctrl+o expand"
	}
}

// contextBar renders a context-usage bar of the given width.
func contextBar(pct float64) string {
	const width = 8
	filled := int(pct / 100 * width)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("▮", filled) + strings.Repeat("▯", width-filled)
}

// truncateJoin joins parts with sep and truncates the result to width cells.
func truncateJoin(parts []string, width int, sep string) string {
	if width <= 0 {
		return ""
	}
	joined := strings.Join(parts, sep)
	if ansi.StringWidth(joined) <= width {
		return joined
	}
	// Drop segments from the right until the rest fits.
	for len(parts) > 1 {
		parts = parts[:len(parts)-1]
		joined = strings.Join(parts, sep)
		if ansi.StringWidth(joined)+ansi.StringWidth("…") <= width {
			return joined + "…"
		}
	}
	return ansi.Truncate(joined, width, "")
}

// fitLines returns exactly h lines, padding with blanks or truncating.
func fitLines(lines []string, h int) []string {
	if h <= 0 {
		return nil
	}
	out := make([]string, 0, h)
	out = append(out, lines...)
	for len(out) < h {
		out = append(out, "")
	}
	return out[:h]
}

// padLine truncates a line to width cells and pads it with spaces.
func padLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = ansi.Truncate(line, width, "")
	if w := ansi.StringWidth(line); w < width {
		line += strings.Repeat(" ", width-w)
	}
	return line
}
