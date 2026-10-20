//go:build !notui

package dialog

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/sanitize"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Setting is one editable integer row in the settings dialog. Key is the dotted
// config key the value is saved under, so the caller can persist a value without
// knowing which row the user changed.
type Setting struct {
	Key   string
	Label string
	Value int
	Min   int
	Max   int
}

// settingsDialog edits a list of integer settings. Value is []Setting with the
// clamped, edited values.
type settingsDialog struct {
	title     string
	rows      []Setting
	cursor    int
	buf       string // digits typed for the current row; "" means use Value
	done      bool
	cancelled bool
	value     []Setting
}

// NewSettings builds the settings dialog.
func NewSettings(title string, rows []Setting) Dialog {
	if title == "" {
		title = "Settings"
	}
	return &settingsDialog{title: title, rows: rows}
}

// ID is empty for a local dialog.
func (s *settingsDialog) ID() string { return "" }

// value is a row's value after parsing the typed buffer, clamped to its range.
func (s *settingsDialog) rowValue(i int) int {
	n := s.rows[i].Value
	if i == s.cursor && s.buf != "" {
		if v, err := strconv.Atoi(s.buf); err == nil {
			n = v
		}
	}
	return clampSetting(n, s.rows[i].Min, s.rows[i].Max)
}

// commit folds the current row's typed value back and clears the buffer.
func (s *settingsDialog) commit() {
	if s.cursor >= 0 && s.cursor < len(s.rows) {
		s.rows[s.cursor].Value = s.rowValue(s.cursor)
	}
	s.buf = ""
}

// step moves the current row by delta and clamps.
func (s *settingsDialog) step(delta int) {
	s.commit()
	r := &s.rows[s.cursor]
	r.Value = clampSetting(r.Value+delta, r.Min, r.Max)
}

// HandleKey implements the settings keys: up/down move, +/- or left/right step,
// digits edit, enter saves, esc cancels.
func (s *settingsDialog) HandleKey(k tea.KeyPressMsg) Result {
	switch key := k.Keystroke(); key {
	case "esc", "ctrl+c":
		return Result{Done: true, Cancelled: true}
	case "enter":
		s.commit()
		s.value = append([]Setting(nil), s.rows...)
		s.done = true
		return Result{Done: true, Value: s.value}
	case "up", "k":
		s.commit()
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		s.commit()
		if s.cursor < len(s.rows)-1 {
			s.cursor++
		}
	case "left", "-":
		s.step(-1)
	case "right", "+", "=":
		s.step(1)
	case "backspace":
		if s.buf != "" {
			s.buf = s.buf[:len(s.buf)-1]
		}
	default:
		if len([]rune(key)) == 1 && key >= "0" && key <= "9" {
			s.buf += key
		}
	}
	return Result{}
}

// Tick does nothing for a settings dialog.
func (s *settingsDialog) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (s *settingsDialog) Size(maxW, maxH int) (int, int) {
	w := 52
	for _, r := range s.rows {
		if n := ansi.StringWidth(r.Label) + 20; n > w {
			w = n
		}
	}
	if len(s.title)+8 > w {
		w = len(s.title) + 8
	}
	return min(w, maxW), min(len(s.rows)+5, maxH)
}

// View renders the framed settings list.
func (s *settingsDialog) View(w, h int, st styles.Styles) string {
	inner := w - 2
	body := make([]string, 0, len(s.rows))
	for i, r := range s.rows {
		marker := "  "
		if i == s.cursor {
			marker = "> "
		}
		line := marker + sanitize.Text(r.Label)
		val := strconv.Itoa(s.rowValue(i))
		if pad := inner - ansi.StringWidth(line) - len(val) - 1; pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		line += val
		if i != s.cursor {
			line = st.Muted.Render(line)
		}
		body = append(body, ansi.Truncate(line, inner, "…"))
	}
	return frame(s.title, body, "↑↓ move · type or +/- · enter save · esc cancel", w, h, st)
}

// clampSetting clamps v to [lo, hi]. A zero Max means no upper bound.
func clampSetting(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if hi > 0 && v > hi {
		return hi
	}
	return v
}
