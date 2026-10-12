//go:build !notui

package dialog

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// selectDialog is a filtered single or multi select. Value is []string ids in
// option order.
type selectDialog struct {
	id        string
	title     string
	options   []proto.UIOption
	multi     bool
	filter    string
	cursor    int
	scroll    int
	toggled   map[string]bool
	done      bool
	cancelled bool
	value     []string
}

// NewSelect builds a select dialog from a ui_request.
func NewSelect(req proto.UIRequest) Dialog {
	return &selectDialog{
		id: req.ID, title: req.Title, options: req.Options, multi: req.Multi,
		toggled: map[string]bool{},
	}
}

// ID returns the request id.
func (s *selectDialog) ID() string { return s.id }

// visible returns the options matching the filter.
func (s *selectDialog) visible() []proto.UIOption {
	if s.filter == "" {
		return s.options
	}
	q := strings.ToLower(s.filter)
	var out []proto.UIOption
	for _, o := range s.options {
		if strings.Contains(strings.ToLower(o.Label), q) || strings.Contains(strings.ToLower(o.Description), q) {
			out = append(out, o)
		}
	}
	return out
}

// chosenID returns the id under the cursor.
func (s *selectDialog) chosenID() string {
	vis := s.visible()
	if s.cursor < 0 || s.cursor >= len(vis) {
		return ""
	}
	return vis[s.cursor].ID
}

// finish records the answer.
func (s *selectDialog) finish(cancelled bool) Result {
	s.done, s.cancelled = true, cancelled
	if cancelled {
		return Result{Done: true, Cancelled: true, Value: []string(nil)}
	}
	if s.multi {
		for _, o := range s.options {
			if s.toggled[o.ID] {
				s.value = append(s.value, o.ID)
			}
		}
	} else if id := s.chosenID(); id != "" {
		s.value = []string{id}
	}
	return Result{Done: true, Value: s.value}
}

// HandleKey implements the select keys.
func (s *selectDialog) HandleKey(k tea.KeyPressMsg) Result {
	vis := s.visible()
	switch k.Keystroke() {
	case "esc", "ctrl+c":
		return s.finish(true)
	case "enter":
		if !s.multi {
			if len(vis) == 0 {
				return Result{}
			}
			return s.finish(false)
		}
		return s.finish(false)
	case "space":
		if s.multi {
			if id := s.chosenID(); id != "" {
				s.toggled[id] = !s.toggled[id]
			}
		}
		return Result{}
	case "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down":
		if s.cursor < len(vis)-1 {
			s.cursor++
		}
	case "pgup":
		s.cursor = max(s.cursor-10, 0)
	case "pgdown":
		s.cursor = min(s.cursor+10, max(len(vis)-1, 0))
	case "backspace":
		if s.filter != "" {
			s.filter = s.filter[:len(s.filter)-1]
			s.cursor = 0
		}
	default:
		if r := k.Keystroke(); len([]rune(r)) == 1 && r >= " " {
			s.filter += r
			s.cursor = 0
		}
	}
	return Result{}
}

// Tick does nothing for a select.
func (s *selectDialog) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (s *selectDialog) Size(maxW, maxH int) (int, int) {
	w := min(max(len(s.title)+4, 44), maxW)
	h := min(len(s.visible())+6, maxH)
	return w, max(h, 7)
}

// View renders the framed dialog.
func (s *selectDialog) View(w, h int, st styles.Styles) string {
	inner := w - 2
	rows := h - 4 // filter row, blank, hint, plus two borders
	vis := s.visible()
	var body []string
	body = append(body, st.Muted.Render("filter: "+safe(s.filter)))
	if len(vis) == 0 {
		body = append(body, "no matches")
	} else {
		if s.cursor < s.scroll {
			s.scroll = s.cursor
		}
		if s.cursor >= s.scroll+rows {
			s.scroll = s.cursor - rows + 1
		}
		end := min(s.scroll+rows, len(vis))
		for i := s.scroll; i < end; i++ {
			o := vis[i]
			marker := "  "
			if i == s.cursor {
				marker = "> "
			}
			if s.multi && s.toggled[o.ID] {
				marker = "[x] "
			} else if s.multi {
				marker = "[ ] "
			}
			line := marker + safe(o.Label)
			if o.Description != "" {
				pad := max(inner-ansi.StringWidth(line)-ansi.StringWidth(o.Description)-2, 1)
				line += strings.Repeat(" ", pad) + st.Dim.Render(safe(o.Description))
			}
			body = append(body, line)
		}
	}
	hint := "↑↓ move · enter select · esc cancel"
	if s.multi {
		hint = "space toggle · enter confirm · esc cancel"
	}
	return frame(s.title, body, hint, w, h, st)
}
