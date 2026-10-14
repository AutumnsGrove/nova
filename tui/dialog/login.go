//go:build !notui

package dialog

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
	return min(max(len(l.title)+4, 48), maxW), min(len(l.items)+4, maxH)
}

// View renders the framed list. The selected row is marked with `>`.
func (l *listPicker) View(w, h int, st styles.Styles) string {
	body := make([]string, 0, len(l.items))
	for i, item := range l.items {
		marker := "  "
		if i == l.cursor {
			marker = "> "
		}
		line := marker + safe(item.label)
		if item.note != "" {
			line += "  " + st.Muted.Render(safe(item.note))
		}
		body = append(body, ansi.Truncate(line, max(w-2, 1), "…"))
	}
	return frame(l.title, body, "enter select · esc cancel", w, h, st)
}
