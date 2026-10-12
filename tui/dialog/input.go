//go:build !notui

package dialog

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Paster is a dialog that accepts pasted text.
type Paster interface{ Paste(text string) }

// inputDialog is a single-line text input. Value is the submitted string. The
// request's Text is the initial default.
type inputDialog struct {
	id          string
	title       string
	placeholder string
	buf         []rune
	cursor      int
	done        bool
	value       string
}

// NewInput builds an input dialog from a ui_request.
func NewInput(req proto.UIRequest) Dialog {
	return &inputDialog{
		id: req.ID, title: req.Title, placeholder: req.Placeholder,
		buf: []rune(req.Text), cursor: len([]rune(req.Text)),
	}
}

// ID returns the request id.
func (d *inputDialog) ID() string { return d.id }

// insert adds text at the cursor.
func (d *inputDialog) insert(text string) {
	r := []rune(text)
	d.buf = append(d.buf[:d.cursor], append(r, d.buf[d.cursor:]...)...)
	d.cursor += len(r)
}

// finish records the submitted text.
func (d *inputDialog) finish(cancelled bool) Result {
	d.done = true
	if !cancelled {
		d.value = string(d.buf)
	}
	return Result{Done: true, Cancelled: cancelled, Value: d.value}
}

// HandleKey implements the input keys.
func (d *inputDialog) HandleKey(k tea.KeyPressMsg) Result {
	switch k.Keystroke() {
	case "enter":
		return d.finish(false)
	case "esc", "ctrl+c":
		return d.finish(true)
	case "backspace":
		if d.cursor > 0 {
			d.buf = append(d.buf[:d.cursor-1], d.buf[d.cursor:]...)
			d.cursor--
		}
	case "delete":
		if d.cursor < len(d.buf) {
			d.buf = append(d.buf[:d.cursor], d.buf[d.cursor+1:]...)
		}
	case "left":
		if d.cursor > 0 {
			d.cursor--
		}
	case "right":
		if d.cursor < len(d.buf) {
			d.cursor++
		}
	case "home":
		d.cursor = 0
	case "end":
		d.cursor = len(d.buf)
	default:
		if r := k.Keystroke(); len([]rune(r)) == 1 && r >= " " {
			d.insert(r)
		}
	}
	return Result{}
}

// Paste inserts pasted text, joining newlines with spaces.
func (d *inputDialog) Paste(text string) {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	d.insert(text)
}

// Tick does nothing for an input.
func (d *inputDialog) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (d *inputDialog) Size(maxW, maxH int) (int, int) {
	w := min(max(len([]rune(d.placeholder))+8, 40), maxW)
	return w, min(6, maxH)
}

// View renders the framed dialog.
func (d *inputDialog) View(w, h int, st styles.Styles) string {
	shown := string(d.buf)
	if shown == "" && d.placeholder != "" {
		shown = st.Dim.Render(safe(d.placeholder))
	} else {
		runes := []rune(shown)
		pos := min(d.cursor, len(runes))
		shown = string(runes[:pos]) + "▏" + string(runes[pos:])
	}
	row := "> " + shown
	body := []string{row}
	return frame(d.title, body, "enter submit · esc cancel", w, max(h, 4), st)
}
