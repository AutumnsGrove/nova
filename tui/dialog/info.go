//go:build !notui

package dialog

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// infoDialog is a read-only, scrollable text overlay.
type infoDialog struct {
	title  string
	lines  []string
	scroll int
}

// NewInfo builds a read-only overlay with a title and body lines.
func NewInfo(title string, lines []string) Dialog {
	return &infoDialog{title: title, lines: lines}
}

// ID is empty for a local dialog.
func (d *infoDialog) ID() string { return "" }

// HandleKey closes on esc/enter/q and scrolls with the arrows.
func (d *infoDialog) HandleKey(k tea.KeyPressMsg) Result {
	switch key := k.Keystroke(); key {
	case "esc", "ctrl+c", "q", "enter":
		return Result{Done: true}
	case "up":
		if d.scroll > 0 {
			d.scroll--
		}
	case "down":
		d.scroll++
	case "pgup":
		d.scroll = max(d.scroll-10, 0)
	case "pgdown":
		d.scroll += 10
	case "home":
		d.scroll = 0
	}
	return Result{}
}

// Tick does nothing for a static overlay.
func (d *infoDialog) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (d *infoDialog) Size(maxW, maxH int) (int, int) {
	width := 0
	for _, line := range d.lines {
		width = max(width, ansi.StringWidth(line))
	}
	return min(max(len(d.title)+8, width+4), maxW), min(len(d.lines)+5, maxH)
}

// View renders the framed overlay.
func (d *infoDialog) View(w, height int, st styles.Styles) string {
	inner, rows := w-2, height-3
	start, end := window(d.scroll+rows-1, 0, rows, len(d.lines))
	d.scroll = start
	body := []string{st.Bold.Render(d.title), ""}
	for _, line := range d.lines[start:end] {
		body = append(body, ansi.Truncate(line, inner, "…"))
	}
	return frame(d.title, body, "esc close", w, height, st)
}
