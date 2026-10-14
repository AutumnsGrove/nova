//go:build !notui

// Package dialog is every TUI overlay that is not the transcript or the
// editor: the approval prompt, the extension UI dialogs, the multi-question
// form, the pickers, and help. Dialogs are pure: they read keys and time and
// render text; they never do I/O.
package dialog

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// Result is one key's outcome. Done reports the dialog closed; Value is the
// dialog's answer, defined by each constructor. Retry asks the caller to run
// the same flow again (a failed sign-in the user chose to retry).
type Result struct {
	Done      bool
	Cancelled bool
	Retry     bool
	Value     any
}

// Dialog is one overlay. View returns exactly h lines of width w, including
// any border, title, and hint.
type Dialog interface {
	ID() string
	HandleKey(k tea.KeyPressMsg) Result
	Size(maxW, maxH int) (w, h int)
	View(w, h int, st styles.Styles) string
	Tick(now time.Time) bool
}

// smallScreener lets a confirm keep working on a screen too small for a box.
type smallScreener interface{ smallScreen() string }

// screen minimums below which the box layout gives way.
const (
	minScreenW = 30
	minScreenH = 8
)

// Overlay is a stack of dialogs. The top one has focus.
type Overlay struct {
	stack  []Dialog
	notice string
}

// NewOverlay returns an empty overlay.
func NewOverlay() *Overlay { return &Overlay{} }

// Push adds a dialog on top.
func (o *Overlay) Push(d Dialog) { o.stack = append(o.stack, d) }

// Pop removes and returns the top dialog.
func (o *Overlay) Pop() Dialog {
	if len(o.stack) == 0 {
		return nil
	}
	d := o.stack[len(o.stack)-1]
	o.stack = o.stack[:len(o.stack)-1]
	return d
}

// Top returns the focused dialog, or nil.
func (o *Overlay) Top() Dialog {
	if len(o.stack) == 0 {
		return nil
	}
	return o.stack[len(o.stack)-1]
}

// Len reports the stack depth.
func (o *Overlay) Len() int { return len(o.stack) }

// HandleKey sends a key to the top dialog and pops it when it is done. It
// returns the result and the dialog that produced it.
func (o *Overlay) HandleKey(k tea.KeyPressMsg) (Result, Dialog) {
	top := o.Top()
	if top == nil {
		return Result{}, nil
	}
	r := top.HandleKey(k)
	if r.Done {
		o.Pop()
	}
	return r, top
}

// Tick advances the top dialog's countdown.
func (o *Overlay) Tick(now time.Time) bool {
	if top := o.Top(); top != nil {
		return top.Tick(now)
	}
	return false
}

// TakeNotice returns and clears the last overlay warning.
func (o *Overlay) TakeNotice() string {
	n := o.notice
	o.notice = ""
	return n
}

// Compose centres the top dialog over base and returns exactly h lines of
// width w. On a screen too small for a box, a confirm degrades to one line and
// any other dialog is cancelled with a notice.
func (o *Overlay) Compose(base string, w, h int, st styles.Styles) string {
	if h <= 0 {
		return ""
	}
	top := o.Top()
	if top == nil {
		return strings.Join(normalizeKeep(base, w, h), "\n")
	}
	lines := normalize(base, w, h)
	if w < minScreenW || h < minScreenH {
		if s, ok := top.(smallScreener); ok {
			row := (h - 1) / 2
			lines[row] = centerLine(s.smallScreen(), w)
		} else {
			o.notice = "dialog cancelled: the screen is too small"
			o.Pop()
		}
		return strings.Join(lines, "\n")
	}
	bw := min(max(w*4/5, minScreenW), w)
	bh := min(h*4/5, h)
	bw, bh = top.Size(bw, bh)
	bw, bh = min(bw, w), min(bh, h)
	box := strings.Split(top.View(bw, bh, st), "\n")
	ox, oy := (w-bw)/2, (h-bh)/2
	for i := 0; i < bh && i < len(box); i++ {
		y := oy + i
		if y < 0 || y >= len(lines) {
			continue
		}
		lines[y] = splice(lines[y], ox, clipPad(box[i], bw), w)
	}
	return strings.Join(lines, "\n")
}

// normalizeKeep is normalize without stripping ANSI, for a base with no dialog.
func normalizeKeep(s string, w, h int) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i := range lines {
		line := ansi.Truncate(lines[i], max(w, 0), "")
		lines[i] = line + strings.Repeat(" ", max(w-ansi.StringWidth(line), 0))
	}
	return lines
}

// normalize returns exactly h lines, each padded or clipped to width w.
func normalize(s string, w, h int) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = clipPad(lines[i], w)
	}
	return lines
}

// splice overwrites cells [x, x+len(box)) of line with box.
func splice(line string, x int, box string, w int) string {
	left := clipPad(ansi.Cut(line, 0, x), x)
	right := ansi.Cut(line, x+ansi.StringWidth(box), w)
	return clipPad(left+box+right, w)
}

// clipPad clips s to width cells and pads it with spaces.
func clipPad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = ansi.Truncate(ansi.Strip(s), width, "")
	return s + strings.Repeat(" ", max(width-ansi.StringWidth(s), 0))
}

// window returns the [start,end) rows that keep cursor visible in a list of n
// rows, given the current scroll offset.
func window(cursor, scroll, rows, n int) (int, int) {
	if cursor < scroll {
		scroll = cursor
	}
	if cursor >= scroll+rows {
		scroll = cursor - rows + 1
	}
	if scroll > n-rows {
		scroll = n - rows
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll, min(scroll+rows, n)
}

// centerLine centres s in a line of width.
func centerLine(s string, width int) string {
	s = ansi.Truncate(s, width, "…")
	left := max((width-ansi.StringWidth(s))/2, 0)
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", max(width-left-ansi.StringWidth(s), 0))
}

// frame draws a bordered box with a title in the top edge and a hint in the
// bottom-left. body is clipped to the inner height.
func frame(title string, body []string, hint string, w, h int, st styles.Styles) string {
	if w < 2 || h < 2 {
		return ""
	}
	inner := w - 2
	out := make([]string, 0, h)
	out = append(out, "┌"+edge(" "+title+" ", inner)+"┐")
	rows := h - 2
	hintRows := 0
	if hint != "" && rows > 0 {
		hintRows = 1
	}
	contentRows := rows - hintRows
	clipped := len(body) > contentRows
	for i := 0; i < contentRows; i++ {
		line := ""
		switch {
		case clipped && i == contentRows-1:
			line = st.Dim.Render("… more")
		case i < len(body):
			line = body[i]
		}
		out = append(out, "│"+clipPad(line, inner)+"│")
	}
	if hintRows == 1 {
		out = append(out, "│"+clipPad(st.Dim.Render(ansi.Strip(hint)), inner)+"│")
	}
	out = append(out, "└"+strings.Repeat("─", inner)+"┘")
	return strings.Join(out, "\n")
}

// edge renders label followed by dashes to width.
func edge(label string, width int) string {
	if width <= 0 {
		return ""
	}
	label = ansi.Truncate(label, width, "…")
	return label + strings.Repeat("─", width-ansi.StringWidth(label))
}

// wrap wraps plain text to width, returning at least one line.
func wrap(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	if s == "" {
		return []string{""}
	}
	return strings.Split(ansi.Wordwrap(s, width, ""), "\n")
}

// prettyArgs renders tool arguments as indented JSON, capped at maxLines.
func prettyArgs(raw json.RawMessage, maxLines int) []string {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []string{string(raw)}
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return []string{string(raw)}
	}
	lines := strings.Split(string(pretty), "\n")
	if maxLines > 0 && len(lines) > maxLines {
		more := len(lines) - (maxLines - 1)
		lines = append(lines[:maxLines-1], fmt.Sprintf("… %d more", more))
	}
	return lines
}

// splitArgsRunes caps a raw string for display, stripping control sequences.
func safe(s string) string { return ansi.Strip(s) }
