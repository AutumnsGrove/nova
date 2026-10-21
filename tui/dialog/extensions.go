//go:build !notui

package dialog

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/sanitize"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// ExtInfo is one extension's supervision status.
type ExtInfo struct {
	Name      string
	Version   string
	State     string // running | failed | disabled
	Restarts  int
	LastError string
}

// extView is the /ext list. Value is "reload" after r, else empty.
type extView struct {
	items  []ExtInfo
	cursor int
	scroll int
	done   bool
	value  string
}

// NewExtensions builds the extensions view.
func NewExtensions(items []ExtInfo) Dialog { return &extView{items: items} }

// ID is empty for a local dialog.
func (e *extView) ID() string { return "" }

// HandleKey implements the view keys.
func (e *extView) HandleKey(k tea.KeyPressMsg) Result {
	switch key := k.Keystroke(); key {
	case "esc", "ctrl+c", "q":
		return Result{Done: true, Cancelled: true}
	case "r":
		e.done, e.value = true, "reload"
		return Result{Done: true, Value: "reload"}
	case "up":
		if e.cursor > 0 {
			e.cursor--
		}
	case "down":
		if e.cursor < len(e.items)-1 {
			e.cursor++
		}
	}
	return Result{}
}

// Tick does nothing for the view.
func (e *extView) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (e *extView) Size(maxW, maxH int) (int, int) {
	return min(max(len("Extensions")+8, 56), maxW), min(len(e.items)+5, maxH)
}

// View renders the framed list.
func (e *extView) View(w, h int, st styles.Styles) string {
	inner, rows := w-2, h-3
	var body []string
	if len(e.items) == 0 {
		body = append(body, "no extensions")
	} else {
		start, end := window(e.cursor, e.scroll, rows, len(e.items))
		e.scroll = start
		for i := start; i < end; i++ {
			body = append(body, e.line(i, inner, st))
		}
	}
	return frame("Extensions", body, "r reload · esc close", w, h, st)
}

// line renders one extension row.
func (e *extView) line(i, inner int, st styles.Styles) string {
	it := e.items[i]
	marker := "  "
	if i == e.cursor {
		marker = "> "
	}
	state := it.State
	style := st.Success
	switch it.State {
	case "failed":
		style = st.Error
	case "disabled":
		style = st.Muted
	}
	line := marker + sanitize.Text(it.Name)
	if it.Version != "" {
		line += " " + st.Muted.Render(sanitize.Text(it.Version))
	}
	line += " " + style.Render(state)
	if it.Restarts > 0 {
		line += st.Dim.Render(fmt.Sprintf(" restarts:%d", it.Restarts))
	}
	if it.LastError != "" {
		line += " " + st.Error.Render(sanitize.Text(it.LastError))
	}
	return ansi.Truncate(line, inner, "…")
}

// KeyHelp is one help entry.
type KeyHelp struct {
	Key         string
	Description string
}

// helpDialog is the scrollable key and command reference.
type helpDialog struct {
	keys   []KeyHelp
	cmds   []string
	scroll int
}

// NewHelp builds the help dialog.
func NewHelp(keys []KeyHelp, cmds []string) Dialog {
	return &helpDialog{keys: keys, cmds: cmds}
}

// ID is empty for a local dialog.
func (h *helpDialog) ID() string { return "" }

// lines builds every help line.
func (h *helpDialog) lines(st styles.Styles) []string {
	out := []string{st.Bold.Render("keys")}
	for _, k := range h.keys {
		out = append(out, "  "+st.Warm.Render(sanitize.Text(k.Key))+"  "+sanitize.Text(k.Description))
	}
	out = append(out, "", st.Bold.Render("commands"))
	for _, c := range h.cmds {
		out = append(out, "  /"+sanitize.Text(c))
	}
	return out
}

// HandleKey implements the help keys.
func (h *helpDialog) HandleKey(k tea.KeyPressMsg) Result {
	switch key := k.Keystroke(); key {
	case "esc", "ctrl+c", "q", "enter", "?":
		return Result{Done: true}
	case "up":
		if h.scroll > 0 {
			h.scroll--
		}
	case "down":
		h.scroll++
	case "pgup":
		h.scroll = max(h.scroll-10, 0)
	case "pgdown":
		h.scroll += 10
	case "home":
		h.scroll = 0
	}
	return Result{}
}

// Tick does nothing for help.
func (h *helpDialog) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (h *helpDialog) Size(maxW, maxH int) (int, int) {
	return min(max(len("Help")+8, 56), maxW), min(len(h.keys)+len(h.cmds)+6, maxH)
}

// View renders the framed help.
func (h *helpDialog) View(w, height int, st styles.Styles) string {
	inner, rows := w-2, height-3
	lines := h.lines(st)
	start, end := window(h.scroll+rows-1, 0, rows, len(lines))
	h.scroll = start
	var body []string
	for _, l := range lines[start:end] {
		body = append(body, ansi.Truncate(l, inner, "…"))
	}
	return frame("Help", body, "↑↓ scroll · esc close", w, height, st)
}
