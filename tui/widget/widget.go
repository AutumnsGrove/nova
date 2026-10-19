//go:build !notui

// Package widget holds the extension-published pieces of the TUI: named
// widgets in three placements, status segments for the footer, and toasts.
// Layout belongs to the TUI; an extension only chooses a placement and content.
package widget

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// Content is one widget's body. Kind selects which fields are read.
type Content struct {
	Kind    string // text | list | table | progress
	Lines   []string
	Items   []Item
	Columns []string
	Rows    [][]string
	Label   string
	Value   float64
}

// Item is one list row.
type Item struct {
	Text   string
	State  string // todo | doing | done
	Detail string
}

// Segment is one status segment. Style is a theme token name.
type Segment struct {
	Text  string
	Style string
}

// Toast lifetimes by level, and the layout caps.
const (
	infoLife       = 4 * time.Second
	warnLife       = 8 * time.Second
	maxWidgetLines = 8
	maxToasts      = 3
)

// widgetEntry, statusEntry, and toast keep first-appearance order and expiry.
type widgetEntry struct {
	key, placement string
	content        *Content
}
type statusEntry struct {
	key  string
	segs []Segment
}
type toast struct {
	text  string
	level string
	at    time.Time
}

// Store holds every extension UI slot. It is safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	widgets []*widgetEntry
	status  []*statusEntry
	toasts  []*toast
	notice  string
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{} }

// SetWidget sets or clears the widget under key. A nil content clears it; an
// update keeps the widget's first-appearance position.
func (s *Store) SetWidget(key, placement string, c *Content) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c != nil && !validKind(c.Kind) {
		s.notice = "widget " + key + ": unknown kind " + c.Kind
		return
	}
	for i, w := range s.widgets {
		if w.key != key {
			continue
		}
		if c == nil {
			s.widgets = append(s.widgets[:i], s.widgets[i+1:]...)
		} else {
			w.content, w.placement = c, placement
		}
		return
	}
	if c != nil {
		s.widgets = append(s.widgets, &widgetEntry{key: key, placement: placement, content: c})
	}
}

// SetStatus sets or clears the status segments under key. A nil slice clears.
func (s *Store) SetStatus(key string, segs []Segment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.status {
		if e.key != key {
			continue
		}
		if segs == nil {
			s.status = append(s.status[:i], s.status[i+1:]...)
		} else {
			e.segs = segs
		}
		return
	}
	if segs != nil {
		s.status = append(s.status, &statusEntry{key: key, segs: segs})
	}
}

// Notify records a toast. Identical text refreshes the existing toast instead
// of stacking a duplicate.
func (s *Store) Notify(text, level string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.toasts {
		if t.text == text {
			t.at, t.level = now, level
			return
		}
	}
	s.toasts = append(s.toasts, &toast{text: text, level: level, at: now})
}

// TakeNotice returns and clears the last warning, such as an unknown content
// kind.
func (s *Store) TakeNotice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.notice
	s.notice = ""
	return n
}

// Height reports how many lines Lines would return for placement.
func (s *Store) Height(placement string, width, maxLines int) int {
	return len(s.render(placement, width, maxLines, styles.Styles{}))
}

// Lines renders the widgets at placement, capped at maxLines and eight lines.
func (s *Store) Lines(placement string, width, maxLines int, st styles.Styles) []string {
	return s.render(placement, width, maxLines, st)
}

// render builds the placement's lines and applies the height cap.
func (s *Store) render(placement string, width, maxLines int, st styles.Styles) []string {
	s.mu.Lock()
	var entries []*widgetEntry
	for _, w := range s.widgets {
		if w.placement == placement {
			entries = append(entries, w)
		}
	}
	s.mu.Unlock()

	var lines []string
	for _, w := range entries {
		lines = append(lines, contentLines(w.content, width, st)...)
	}
	cap := maxLines
	if cap > maxWidgetLines || cap <= 0 {
		cap = maxWidgetLines
	}
	if len(lines) > cap {
		lines = append(lines[:cap-1], fmt.Sprintf("… %d more", len(lines)-cap))
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(width, 0), "")
	}
	return lines
}

// contentLines renders one widget body.
func contentLines(c *Content, width int, st styles.Styles) []string {
	if width <= 0 {
		return nil
	}
	switch c.Kind {
	case "text":
		return textLines(c.Lines, width, st.Text)
	case "list":
		return listLines(c.Items, width, st)
	case "table":
		return tableLines(c.Columns, c.Rows, width, st)
	case "progress":
		return progressLines(c.Label, c.Value, width, st)
	default:
		return nil
	}
}

// textLines wraps each line to the width.
func textLines(lines []string, width int, style lipgloss.Style) []string {
	var out []string
	for _, line := range lines {
		for _, wrapped := range strings.Split(ansi.Wordwrap(ansi.Strip(line), width, ""), "\n") {
			out = append(out, style.Render(wrapped))
		}
	}
	return out
}

// listLines renders list items with a state glyph.
func listLines(items []Item, width int, st styles.Styles) []string {
	glyphs := map[string]string{"todo": "☐", "doing": "◐", "done": "☑"}
	var out []string
	for _, it := range items {
		glyph := glyphs[it.State]
		if glyph == "" {
			glyph = "•"
		}
		style := st.Text
		if it.State == "doing" {
			style = st.Warm
		}
		line := style.Render(glyph + " " + ansi.Strip(it.Text))
		if it.Detail != "" {
			line += st.Dim.Render("  " + ansi.Strip(it.Detail))
		}
		out = append(out, ansi.Truncate(line, width, "…"))
	}
	return out
}

// tableLines renders a table with columns sized to fit the width.
func tableLines(columns []string, rows [][]string, width int, st styles.Styles) []string {
	if len(columns) == 0 {
		return nil
	}
	n := len(columns)
	share := max((width-2*(n-1))/n, 1)
	renderRow := func(cells []string, header bool) string {
		parts := make([]string, n)
		for i := 0; i < n; i++ {
			cell := ""
			if i < len(cells) {
				cell = ansi.Truncate(ansi.Strip(cells[i]), share, "…")
			}
			parts[i] = cell + strings.Repeat(" ", max(share-ansi.StringWidth(cell), 0))
		}
		line := strings.TrimRight(strings.Join(parts, "  "), " ")
		if header {
			return st.Bold.Render(line)
		}
		return st.Text.Render(line)
	}
	out := []string{renderRow(columns, true)}
	for _, row := range rows {
		out = append(out, renderRow(row, false))
	}
	return out
}

// progressLines renders a label and a bar with a percentage.
func progressLines(label string, value float64, width int, st styles.Styles) []string {
	value = min(max(value, 0), 1)
	const barWidth = 12
	filled := int(value*barWidth + 0.5)
	line := strings.Repeat("▮", filled) + strings.Repeat("▯", barWidth-filled) +
		fmt.Sprintf(" %d%%", int(value*100+0.5))
	if label != "" {
		line = ansi.Strip(label) + " " + line
	}
	return []string{st.Text.Render(ansi.Truncate(line, width, "…"))}
}

// StatusSegments returns every extension status segment in first-appearance
// order. The status renderer wraps and styles them, so the store only owns the
// order and the text.
func (s *Store) StatusSegments() []Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Segment
	for _, e := range s.status {
		out = append(out, e.segs...)
	}
	return out
}

// Toasts returns the live toasts, newest last, at most three, expired dropped.
func (s *Store) Toasts(now time.Time, width int, st styles.Styles) []string {
	s.mu.Lock()
	var live []*toast
	for _, t := range s.toasts {
		if now.Before(t.expiry()) {
			live = append(live, t)
		}
	}
	s.toasts = live
	if len(s.toasts) > maxToasts {
		s.toasts = s.toasts[len(s.toasts)-maxToasts:]
	}
	kept := append([]*toast(nil), s.toasts...)
	s.mu.Unlock()

	out := make([]string, 0, len(kept))
	for _, t := range kept {
		style := st.Muted
		if t.level == "warn" {
			style = st.Warm
		} else if t.level == "error" {
			style = st.Error
		}
		out = append(out, style.Render(ansi.Truncate(ansi.Strip(t.text), max(width, 0), "…")))
	}
	return out
}

// expiry is when a toast disappears.
func (t *toast) expiry() time.Time {
	if t.level == "warn" || t.level == "error" {
		return t.at.Add(warnLife)
	}
	return t.at.Add(infoLife)
}

// validKind reports whether a content kind is part of the closed set.
func validKind(kind string) bool {
	switch kind {
	case "text", "list", "table", "progress":
		return true
	}
	return false
}
