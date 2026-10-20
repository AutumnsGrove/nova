//go:build !notui

package dialog

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/sanitize"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Answer is one form question's answer. Notes are returned verbatim.
type Answer struct {
	ID      string
	IDs     []string
	Text    string
	Note    string
	Skipped bool
}

// Limits defended here as well as in the ask tool.
const (
	maxQuestions = 8
	maxOptions   = 8
)

// qstate is one question's interactive state.
type qstate struct {
	q        proto.UIQuestion
	opts     []proto.UIOption
	other    int // index of the Other row, or -1
	cursor   int
	sel      map[string]bool
	text     []rune
	free     bool
	note     []rune
	noteMode bool
	skipped  bool
}

// typing reports whether keystrokes edit a text buffer.
func (st *qstate) typing() bool { return st.free || st.noteMode }

// rows is the number of selectable rows.
func (st *qstate) rows() int {
	if st.other >= 0 {
		return len(st.opts) + 1
	}
	return len(st.opts)
}

// form is the multi-question ask dialog. Value is []Answer.
type form struct {
	id        string
	title     string
	states    []*qstate
	tab       int
	allowSkip bool
	done      bool
	cancelled bool
	value     []Answer
}

// NewForm builds the ask form, ignoring extras beyond the limits.
func NewForm(req proto.UIRequest) Dialog {
	f := &form{id: req.ID, title: req.Title, allowSkip: req.AllowSkip}
	if f.title == "" {
		f.title = "Questions"
	}
	for i, q := range req.Questions {
		if i >= maxQuestions {
			break
		}
		st := &qstate{q: q, sel: map[string]bool{}, other: -1}
		for j, o := range q.Options {
			if j >= maxOptions {
				break
			}
			st.opts = append(st.opts, o)
		}
		if q.Kind != "text" && len(st.opts) > 0 {
			if allowOther(q) {
				st.other = len(st.opts)
			}
		} else {
			st.free = true
		}
		f.states = append(f.states, st)
	}
	return f
}

// allowOther treats an unset allow_other as true.
func allowOther(q proto.UIQuestion) bool { return q.AllowOther == nil || *q.AllowOther }

// ID returns the request id.
func (f *form) ID() string { return f.id }

// tabs is the number of tabs: one per question plus Review.
func (f *form) tabs() int { return len(f.states) + 1 }

// current is the active question, or nil on the Review tab.
func (f *form) current() *qstate {
	if f.tab < 0 || f.tab >= len(f.states) {
		return nil
	}
	return f.states[f.tab]
}

// moveTab shifts the active tab, wrapping.
func (f *form) moveTab(delta int) {
	n := f.tabs()
	f.tab = ((f.tab+delta)%n + n) % n
}

// HandleKey implements the form keys.
func (f *form) HandleKey(k tea.KeyPressMsg) Result {
	key := k.Keystroke()
	switch key {
	case "esc":
		return f.finish(true)
	case "tab", "right":
		f.moveTab(1)
		return Result{}
	case "shift+tab", "left":
		f.moveTab(-1)
		return Result{}
	}
	st := f.current()
	if st == nil {
		if key == "enter" {
			return f.finish(false)
		}
		return Result{}
	}
	if st.noteMode {
		f.noteKey(k)
		return Result{}
	}
	if st.free {
		switch key {
		case "enter":
			f.moveTab(1)
		case "backspace":
			if len(st.text) > 0 {
				st.text = st.text[:len(st.text)-1]
			}
		default:
			if r := k.Keystroke(); len([]rune(r)) == 1 && r >= " " {
				st.text = append(st.text, []rune(r)...)
			}
		}
		return Result{}
	}
	switch key {
	case "n":
		st.noteMode = true
	case "s":
		st.skipped = true
		st.sel = map[string]bool{}
		st.text = nil
		f.moveTab(1)
	case "up":
		if !st.typing() && st.cursor > 0 {
			st.cursor--
		}
	case "down":
		if !st.typing() && st.cursor < st.rows()-1 {
			st.cursor++
		}
	case "space":
		switch {
		case st.typing():
			st.text = append(st.text, ' ')
		case st.q.Kind == "multi" && st.cursor < len(st.opts):
			id := st.opts[st.cursor].ID
			st.sel[id] = !st.sel[id]
		}
	case "enter":
		f.enter(st)
	case "backspace":
		if st.typing() && len(st.text) > 0 {
			st.text = st.text[:len(st.text)-1]
		}
	default:
		if r := k.Keystroke(); len([]rune(r)) == 1 && r >= " " {
			if st.free || st.cursor == st.other {
				st.free = true
				st.text = append(st.text, []rune(r)...)
			}
		}
	}
	return Result{}
}

// noteKey edits the note buffer.
func (f *form) noteKey(k tea.KeyPressMsg) {
	st := f.current()
	switch k.Keystroke() {
	case "enter":
		st.noteMode = false
	case "backspace":
		if len(st.note) > 0 {
			st.note = st.note[:len(st.note)-1]
		}
	default:
		if r := k.Keystroke(); len([]rune(r)) == 1 && r >= " " {
			st.note = append(st.note, []rune(r)...)
		}
	}
}

// enter activates the highlighted row or advances a text question.
func (f *form) enter(st *qstate) {
	switch {
	case st.free:
		f.moveTab(1)
	case st.cursor == st.other:
		st.free = true
	default:
		if st.cursor < len(st.opts) && st.q.Kind != "multi" {
			st.sel = map[string]bool{st.opts[st.cursor].ID: true}
		}
		f.moveTab(1)
	}
}

// Paste inserts text into the active text or note buffer, verbatim.
func (f *form) Paste(text string) {
	if st := f.current(); st != nil {
		switch {
		case st.noteMode:
			st.note = append(st.note, []rune(text)...)
		case st.free, st.cursor == st.other:
			st.free = true
			st.text = append(st.text, []rune(text)...)
		}
	}
}

// Tick does nothing for a form.
func (f *form) Tick(time.Time) bool { return false }

// answers builds the answers in question order.
func (f *form) answers(cancelled bool) []Answer {
	out := make([]Answer, 0, len(f.states))
	for _, st := range f.states {
		a := Answer{ID: st.q.ID, Note: string(st.note), Skipped: cancelled || st.skipped}
		switch {
		case a.Skipped:
		case st.free || len(st.opts) == 0:
			a.Text = string(st.text)
			a.Skipped = a.Text == ""
		default:
			for _, o := range st.opts {
				if st.sel[o.ID] {
					a.IDs = append(a.IDs, o.ID)
				}
			}
			a.Skipped = len(a.IDs) == 0
		}
		out = append(out, a)
	}
	return out
}

// finish builds the answers and closes the form.
func (f *form) finish(cancelled bool) Result {
	f.done, f.cancelled = true, cancelled
	f.value = f.answers(cancelled)
	return Result{Done: true, Cancelled: cancelled, Value: f.value}
}

// Size returns the desired box size.
func (f *form) Size(maxW, maxH int) (int, int) {
	w := min(max(len(f.title)+8, 52), maxW)
	h := min(len(f.states)*2+8, maxH)
	return w, max(h, 8)
}

// View renders the framed dialog.
func (f *form) View(w, h int, st styles.Styles) string {
	inner := w - 2
	body := []string{f.tabsLine(st), ""}
	if f.current() == nil {
		body = append(body, f.review(inner, st)...)
	} else {
		q := f.current()
		body = append(body, wrap(st.Bold.Render(sanitize.Text(q.q.Prompt)), inner)...)
		options := f.options(st)
		preview := f.preview()
		switch {
		case preview != "" && w >= 100:
			body = append(body, sideBySide(options, strings.Split(preview, "\n"), inner, st)...)
		case preview != "":
			body = append(body, options...)
			body = append(body, "", st.Muted.Render("preview:"))
			for _, l := range strings.Split(preview, "\n") {
				body = append(body, sanitize.Text(l))
			}
		default:
			body = append(body, options...)
		}
	}
	return frame(f.title, body, f.hint(), w, h, st)
}

// hint is the one-line key hint.
func (f *form) hint() string {
	if f.allowSkip {
		return "tab next · n note · s skip · enter submit · esc cancel"
	}
	return "tab next · n note · enter submit · esc cancel"
}

// tabsLine renders the tab strip.
func (f *form) tabsLine(st styles.Styles) string {
	var parts []string
	for i, qs := range f.states {
		label := "Q" + strconv.Itoa(i+1)
		if qs.skipped {
			label += "~"
		}
		if i == f.tab {
			label = "[" + label + "]"
		}
		parts = append(parts, label)
	}
	review := "Review"
	if f.tab == len(f.states) {
		review = "[Review]"
	}
	parts = append(parts, review)
	return st.Muted.Render(strings.Join(parts, " "))
}

// options renders the current question's selectable rows.
func (f *form) options(st styles.Styles) []string {
	q := f.current()
	if q.noteMode {
		return []string{st.Warm.Render("note: ") + sanitize.Text(string(q.note)) + "▏"}
	}
	if q.free {
		return []string{"> " + sanitize.Text(string(q.text)) + "▏"}
	}
	var out []string
	for i, o := range q.opts {
		out = append(out, optionLine(q, i, o, st))
	}
	if q.other >= 0 {
		out = append(out, optionLine(q, q.other, proto.UIOption{Label: "Other (type something)"}, st))
	}
	if len(q.note) > 0 {
		out = append(out, st.Dim.Render("note: "+sanitize.Text(string(q.note))))
	}
	return out
}

// optionLine renders one option row.
func optionLine(q *qstate, i int, o proto.UIOption, st styles.Styles) string {
	marker := "  "
	if q.q.Kind == "multi" {
		marker = "[ ] "
		if q.sel[o.ID] {
			marker = "[x] "
		}
	} else if i == q.cursor {
		marker = "> "
	}
	line := marker + sanitize.Text(o.Label)
	if o.Description != "" {
		line += "  " + st.Dim.Render(sanitize.Text(o.Description))
	}
	return line
}

// preview returns the highlighted option's preview text.
func (f *form) preview() string {
	q := f.current()
	if q == nil || q.typing() || q.cursor >= len(q.opts) {
		return ""
	}
	return q.opts[q.cursor].Preview
}

// sideBySide overlays right beside left.
func sideBySide(left, right []string, inner int, st styles.Styles) []string {
	leftW := inner / 2
	var out []string
	for i := 0; i < len(left) || i < len(right); i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, clipPad(l, leftW)+st.Muted.Render(" │ ")+sanitize.Text(r))
	}
	return out
}

// review renders every answer.
func (f *form) review(inner int, st styles.Styles) []string {
	var out []string
	for i, a := range f.answers(false) {
		out = append(out, st.Bold.Render("Q"+strconv.Itoa(i+1)+": "+sanitize.Text(f.states[i].q.Prompt)))
		out = append(out, "  "+sanitize.Text(f.answerText(i, a)))
		if a.Note != "" {
			out = append(out, st.Dim.Render("  note: "+a.Note))
		}
	}
	return out
}

// answerText renders one answer with option labels for the review list.
func (f *form) answerText(i int, a Answer) string {
	switch {
	case a.Skipped:
		return "skipped"
	case a.Text != "":
		return a.Text
	}
	st := f.states[i]
	var labels []string
	for _, o := range st.opts {
		if st.sel[o.ID] {
			labels = append(labels, o.Label)
		}
	}
	return strings.Join(labels, ", ")
}
