//go:build !notui

package dialog

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// ModelInfo is one catalog row for the model picker.
type ModelInfo struct {
	ID            string
	Name          string
	Provider      string
	ContextWindow int
	InPerM        float64
	OutPerM       float64
	Current       bool
}

// modelPicker is a filter-as-you-type model list. Value is the chosen id.
type modelPicker struct {
	models  []ModelInfo
	current string
	filter  string
	cursor  int
	scroll  int
	done    bool
	value   string
}

// NewModelPicker builds the model picker. current marks the active model.
func NewModelPicker(models []ModelInfo, current string) Dialog {
	return &modelPicker{models: models, current: current}
}

// ID is empty for a local dialog.
func (m *modelPicker) ID() string { return "" }

// visible returns the models matching the filter.
func (m *modelPicker) visible() []ModelInfo {
	if m.filter == "" {
		return m.models
	}
	q := strings.ToLower(m.filter)
	var out []ModelInfo
	for _, info := range m.models {
		if strings.Contains(strings.ToLower(info.ID), q) || strings.Contains(strings.ToLower(info.Name), q) {
			out = append(out, info)
		}
	}
	return out
}

// chosenID returns the highlighted model id.
func (m *modelPicker) chosenID() string {
	vis := m.visible()
	if m.cursor < 0 || m.cursor >= len(vis) {
		return ""
	}
	return vis[m.cursor].ID
}

// HandleKey implements the picker keys.
func (m *modelPicker) HandleKey(k tea.KeyPressMsg) Result {
	vis := m.visible()
	switch key := k.Keystroke(); key {
	case "esc", "ctrl+c":
		return Result{Done: true, Cancelled: true}
	case "enter":
		if id := m.chosenID(); id != "" {
			m.done, m.value = true, id
			return Result{Done: true, Value: id}
		}
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down":
		if m.cursor < len(vis)-1 {
			m.cursor++
		}
	case "pgup":
		m.cursor = max(m.cursor-10, 0)
	case "pgdown":
		m.cursor = min(m.cursor+10, max(len(vis)-1, 0))
	case "backspace":
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
			m.cursor = 0
		}
	default:
		if len([]rune(key)) == 1 && key >= " " {
			m.filter += key
			m.cursor = 0
		}
	}
	return Result{}
}

// Tick does nothing for a picker.
func (m *modelPicker) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (m *modelPicker) Size(maxW, maxH int) (int, int) {
	return min(max(len("Models")+8, 64), maxW), min(len(m.visible())+6, maxH)
}

// View renders the framed picker.
func (m *modelPicker) View(w, h int, st styles.Styles) string {
	inner, rows := w-2, h-4
	vis := m.visible()
	body := []string{st.Muted.Render("filter: " + safe(m.filter))}
	if len(vis) == 0 {
		body = append(body, "no models")
	} else {
		start, end := window(m.cursor, m.scroll, rows, len(vis))
		m.scroll = start
		for i := start; i < end; i++ {
			body = append(body, modelLine(vis[i], i == m.cursor, m, inner, st))
		}
	}
	return frame("Models", body, "enter select · esc cancel", w, h, st)
}

// modelLine renders one catalog row.
func modelLine(info ModelInfo, selected bool, m *modelPicker, inner int, st styles.Styles) string {
	marker := "  "
	if selected {
		marker = "> "
	}
	line := marker
	if info.ID == m.current || info.Current {
		line += "* "
	} else {
		line += "  "
	}
	line += safe(info.Name)
	if info.ContextWindow > 0 {
		line += "  " + st.Muted.Render(formatContext(info.ContextWindow))
	}
	line += "  " + st.Muted.Render(fmt.Sprintf("in %s out %s", formatPrice(info.InPerM), formatPrice(info.OutPerM)))
	return ansi.Truncate(line, inner, "…")
}

// formatContext renders a token window as 128k or 1M.
func formatContext(n int) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'g', 3, 64) + "M"
	case n >= 1_000:
		return strconv.FormatFloat(float64(n)/1_000, 'g', 3, 64) + "k"
	default:
		return strconv.Itoa(n)
	}
}

// formatPrice renders a per-million price.
func formatPrice(v float64) string { return fmt.Sprintf("$%.2f/M", v) }
