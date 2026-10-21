//go:build !notui

package dialog

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/sanitize"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// ProviderInfo is one row of the provider picker. Value is the provider id.
type ProviderInfo struct {
	ID      string
	Name    string
	Methods []string
	Ready   bool
	Source  string
}

// flowDialog shows a running sign-in: the prompt, the page to open, an
// optional user code, and, for OAuth on a remote machine, an input for the
// redirect URL. Value is the pasted redirect; Retry asks for the same flow
// again after a failure.
type flowDialog struct {
	prompt string
	url    string
	code   string
	paste  bool
	input  []rune
	err    string
	// status, when set, is read on every render, so the line follows the
	// flow ("waiting for the callback", then "received") without the dialog
	// being rebuilt.
	status func() string
}

// NewFlowDialog builds the dialog shown while an OAuth or device flow runs.
// paste adds the input row for a pasted redirect URL.
func NewFlowDialog(prompt, url, code string, paste bool) Dialog {
	return &flowDialog{prompt: prompt, url: url, code: code, paste: paste}
}

// ID is empty for a local dialog.
func (d *flowDialog) ID() string { return "" }

// SetStatus gives the dialog a function that returns the flow's current status
// line, or nil for none.
func (d *flowDialog) SetStatus(f func() string) { d.status = f }

// Fail records a failure so the dialog shows it and offers a retry.
func (d *flowDialog) Fail(msg string) { d.err = msg }

// Paste inserts pasted text, joining newlines with spaces.
func (d *flowDialog) Paste(text string) {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	d.input = append(d.input, []rune(text)...)
}

// HandleKey implements the flow keys.
func (d *flowDialog) HandleKey(k tea.KeyPressMsg) Result {
	key := k.Keystroke()
	if key == "esc" || key == "ctrl+c" {
		return Result{Done: true, Cancelled: true}
	}
	if d.err != "" {
		if key == "enter" {
			return Result{Done: true, Retry: true}
		}
		return Result{}
	}
	if !d.paste {
		return Result{}
	}
	switch key {
	case "enter":
		if text := strings.TrimSpace(string(d.input)); text != "" {
			return Result{Done: true, Value: text}
		}
	case "backspace":
		if len(d.input) > 0 {
			d.input = d.input[:len(d.input)-1]
		}
	case "ctrl+u":
		d.input = nil
	default:
		if r := []rune(key); len(r) == 1 && r[0] >= ' ' {
			d.input = append(d.input, r[0])
		}
	}
	return Result{}
}

// Tick does nothing for a flow dialog.
func (d *flowDialog) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (d *flowDialog) Size(maxW, maxH int) (int, int) {
	width := max(len(d.url)+8, 52)
	if d.status != nil {
		width = max(width, ansi.StringWidth(sanitize.Text(d.status()))+4)
	}
	return min(width, maxW), min(12, maxH)
}

// View renders the framed flow dialog.
func (d *flowDialog) View(w, h int, st styles.Styles) string {
	body := []string{sanitize.Text(d.prompt)}
	if d.url != "" {
		body = append(body, st.Muted.Render("Approve access in your browser. If it did not open, visit:"))
		body = append(body, sanitize.Text(d.url))
	}
	if d.code != "" {
		body = append(body, "code: "+sanitize.Text(d.code))
	}
	if d.status != nil {
		if line := d.status(); line != "" {
			body = append(body, st.Muted.Render(sanitize.Text(line)))
		}
	}
	if d.paste {
		body = append(body, st.Muted.Render("If the browser runs on another machine, paste the URL it was redirected to:"))
		body = append(body, "> "+sanitize.Text(string(d.input))+"▏")
	}
	if d.err != "" {
		body = append(body, st.Error.Render("error: "+sanitize.Text(d.err)), "enter retry · esc close")
	}
	return frame("Sign in", body, "esc cancel", w, max(h, 6), st)
}

// listItem is one row of a generic list picker.
type listItem struct {
	value string
	label string
	note  string
}

// listPicker is a small keyboard list. Value is the highlighted row's value.
type listPicker struct {
	title  string
	items  []listItem
	cursor int
}

// NewProviderPicker builds the /login and /logout provider list.
func NewProviderPicker(title string, items []ProviderInfo) Dialog {
	rows := make([]listItem, len(items))
	for i, it := range items {
		note := strings.Join(it.Methods, ", ")
		if it.Ready {
			note += " · ready"
			if it.Source != "" {
				note += " (" + it.Source + ")"
			}
		}
		rows[i] = listItem{value: it.ID, label: it.Name, note: note}
	}
	return &listPicker{title: title, items: rows}
}

// NewMethodPicker builds the authentication-method list. Value is the chosen
// method label, so the dialog package stays free of auth imports.
func NewMethodPicker(provider string, methods []string) Dialog {
	rows := make([]listItem, len(methods))
	for i, m := range methods {
		rows[i] = listItem{value: m, label: m}
	}
	return &listPicker{title: "Select authentication method for " + provider + ":", items: rows}
}

// ID is empty for a local dialog.
func (l *listPicker) ID() string { return "" }

// HandleKey implements the list keys.
func (l *listPicker) HandleKey(k tea.KeyPressMsg) Result {
	switch k.Keystroke() {
	case "esc", "ctrl+c":
		return Result{Done: true, Cancelled: true}
	case "up":
		if l.cursor > 0 {
			l.cursor--
		}
	case "down":
		if l.cursor < len(l.items)-1 {
			l.cursor++
		}
	case "enter":
		if l.cursor >= 0 && l.cursor < len(l.items) {
			return Result{Done: true, Value: l.items[l.cursor].value}
		}
	}
	return Result{}
}

// Tick does nothing for a list.
func (l *listPicker) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (l *listPicker) Size(maxW, maxH int) (int, int) {
	// Fit the widest row (marker, label, two spaces, note) plus the frame, so a
	// long note such as "ready (config)" is not cut off; the screen still caps it.
	widest := 0
	for _, item := range l.items {
		row := 2 + ansi.StringWidth(sanitize.Text(item.label))
		if item.note != "" {
			row += 2 + ansi.StringWidth(sanitize.Text(item.note))
		}
		widest = max(widest, row)
	}
	return min(max(len(l.title)+4, widest+4, 48), maxW), min(len(l.items)+4, maxH)
}

// View renders the framed list. The selected row is marked with `>`.
func (l *listPicker) View(w, h int, st styles.Styles) string {
	body := make([]string, 0, len(l.items))
	for i, item := range l.items {
		marker := "  "
		if i == l.cursor {
			marker = "> "
		}
		line := marker + sanitize.Text(item.label)
		if item.note != "" {
			line += "  " + st.Muted.Render(sanitize.Text(item.note))
		}
		body = append(body, ansi.Truncate(line, max(w-2, 1), "…"))
	}
	return frame(l.title, body, "enter select · esc cancel", w, h, st)
}
