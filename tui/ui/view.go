//go:build !notui

package ui

import (
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
	v.WindowTitle = m.title
	return v
}

// render builds the region stack, normalises it to exactly the screen size,
// draws toasts, and composes any dialog over it.
func (m *Model) render() string {
	lines := m.baseLines()
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = padLine(lines[i], m.width)
	}
	m.drawToasts(lines)
	return m.overlay.Compose(strings.Join(lines, "\n"), m.width, m.height, m.st)
}

// baseLines stacks the splash banner, header, transcript, widgets, editor,
// and footer.
func (m *Model) baseLines() []string {
	var lines []string
	if m.layout.Banner.H > 0 {
		lines = append(lines, fitLines(strings.Split(m.bannerView(), "\n"), m.layout.Banner.H)...)
	}
	if m.layout.Header.H > 0 {
		lines = append(lines, m.header())
	}
	lines = append(lines, fitLines(strings.Split(m.mainView(), "\n"), m.layout.Main.H)...)
	if m.layout.Above.H > 0 {
		lines = append(lines, fitLines(m.widgets.Lines("above_editor", m.width, m.layout.Above.H, m.st), m.layout.Above.H)...)
	}
	lines = append(lines, fitLines(strings.Split(m.editorView(), "\n"), m.layout.Editor.H)...)
	if m.layout.Below.H > 0 {
		below := m.widgets.Lines("below_editor", m.width, m.layout.Below.H, m.st)
		below = append(below, m.widgets.Lines("sidebar", m.width, m.layout.Below.H, m.st)...)
		lines = append(lines, fitLines(below, m.layout.Below.H)...)
	}
	if m.layout.Footer.H > 0 {
		lines = append(lines, fitLines(m.statusView(), m.layout.Footer.H)...)
	}
	return lines
}

// drawToasts overwrites the blank rows just above the footer with toasts.
func (m *Model) drawToasts(lines []string) {
	toasts := m.widgets.Toasts(m.clock.Now(), m.width, m.st)
	top := len(lines) - m.layout.Footer.H - len(toasts)
	for i, t := range toasts {
		if y := top + i; y >= 0 && y < len(lines) {
			lines[y] = padLine(t, m.width)
		}
	}
}

// header is the one-line session mark. The model, cwd, and branch live in the
// status region, so the header is just the brand.
func (m *Model) header() string {
	return m.st.Spark.Render("✦") + "  " + m.st.Header.Render("nova")
}

// bannerView renders the splash into the top banner region, or "" when the
// splash is off.
func (m *Model) bannerView() string {
	b := m.layout.Banner
	if b.H <= 0 {
		return ""
	}
	out, _, ok := splash.Render(b.W, b.H, m.st, m.profile, m.version, m.splashAt)
	if !ok {
		return ""
	}
	return out
}

// mainView is the transcript with the completion popup drawn over its bottom.
func (m *Model) mainView() string {
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

// editorView is the input area, clipped to the rows the layout gives it. The
// editor renders its whole content; the scroll offset chooses the window, so
// the wheel can scroll it without moving the cursor.
func (m *Model) editorView() string {
	h := m.layout.Editor.H
	if h <= 0 {
		return ""
	}
	lines := strings.Split(m.editor.View(), "\n")
	top := min(max(m.editorScroll, 0), max(len(lines)-h, 0))
	out := lines[top:min(top+h, len(lines))]
	if chips := m.chipLine(); chips != "" {
		// The chip row takes the first editor row, so the area keeps its height.
		out = append([]string{chips}, out...)
		if len(out) > h {
			out = out[:h]
		}
	}
	return strings.Join(out, "\n")
}

// hint is the most relevant key for the current state.
func (m *Model) hint() string {
	switch {
	case m.quitArmed:
		return "ctrl+c again to quit"
	case m.runActive:
		return "esc abort"
	case m.display.ExpandTools:
		return "ctrl+o collapse"
	}
	return "ctrl+o expand"
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
