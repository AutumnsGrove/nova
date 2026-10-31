//go:build !notui

package dialog

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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

	// previewBreak is the width from which a preview sits beside the options
	// instead of below them.
	previewBreak = 100
)

// qstate is one question's interactive state.
type qstate struct {
	q         proto.UIQuestion
	opts      []proto.UIOption
	other     int // index of the Other row, or -1
	allowSkip bool
	cursor    int
	sel       map[string]bool
	text      []rune
	free      bool
	note      []rune
	noteMode  bool
	skipped   bool
}

// textQuestion reports whether the question is answered by typing, with no
// option list.
func (st *qstate) textQuestion() bool { return len(st.opts) == 0 }

// skipRow is the cursor row that skips the question, or -1.
func (st *qstate) skipRow() int {
	if !st.allowSkip {
		return -1
	}
	if st.textQuestion() {
		return 1
	}
	n := len(st.opts)
	if st.other >= 0 {
		n++
	}
	return n
}

// rows is the number of cursor rows.
func (st *qstate) rows() int {
	n := len(st.opts)
	if st.other >= 0 {
		n++
	}
	if st.allowSkip {
		n++
	}
	if st.textQuestion() {
		n = 1
		if st.allowSkip {
			n = 2
		}
	}
	return n
}

// onSkip reports whether the cursor is on the skip row.
func (st *qstate) onSkip() bool { return st.skipRow() >= 0 && st.cursor == st.skipRow() }

// onOther reports whether the cursor is on the free-text row.
func (st *qstate) onOther() bool { return st.other >= 0 && st.cursor == st.other }

// editing reports whether printable keys go to a text buffer.
func (st *qstate) editing() bool {
	if st.noteMode {
		return true
	}
	if st.textQuestion() {
		return st.cursor == 0
	}
	return st.free && st.onOther()
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
		st := &qstate{q: q, sel: map[string]bool{}, other: -1, allowSkip: req.AllowSkip}
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

// sheet marks the form as a full-width surface anchored to the bottom of the
// screen rather than a centred box (see Overlay.Compose).
func (f *form) sheet() {}

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

// moveTab shifts the active tab, wrapping, and leaves note mode behind.
func (f *form) moveTab(delta int) {
	if st := f.current(); st != nil {
		st.noteMode = false
	}
	n := f.tabs()
	f.tab = ((f.tab+delta)%n + n) % n
}

// HandleKey implements the form keys.
func (f *form) HandleKey(k tea.KeyPressMsg) Result {
	key := k.Keystroke()
	st := f.current()
	if key == "esc" {
		if st != nil && st.noteMode {
			st.noteMode = false
			return Result{}
		}
		return f.finish(true)
	}
	if st != nil && st.noteMode {
		f.noteKey(k)
		return Result{}
	}
	switch key {
	case "tab":
		f.moveTab(1)
		return Result{}
	case "shift+tab":
		f.moveTab(-1)
		return Result{}
	case "ctrl+n":
		if st != nil {
			st.noteMode = true
		}
		return Result{}
	}
	if st == nil {
		if key == "enter" {
			return f.finish(false)
		}
		return Result{}
	}
	if st.editing() {
		f.textKey(st, k)
		return Result{}
	}
	switch key {
	case "up":
		if st.cursor > 0 {
			st.cursor--
		}
	case "down":
		if st.cursor < st.rows()-1 {
			st.cursor++
		}
	case "left":
		f.moveTab(-1)
	case "right":
		f.moveTab(1)
	case "space":
		if st.q.Kind == "multi" && st.cursor < len(st.opts) {
			f.toggle(st, st.cursor)
		}
	case "enter":
		f.enter(st)
	case "backspace":
		// Nothing to delete outside a text buffer.
	default:
		if r := k.Keystroke(); len([]rune(r)) == 1 && r >= " " {
			if r[0] >= '1' && r[0] <= '9' && !st.textQuestion() {
				if i := int(r[0] - '1'); i < len(st.opts) {
					f.pick(st, i)
					return Result{}
				}
			}
			f.typeRune(st, []rune(r)[0])
		}
	}
	return Result{}
}

// textKey edits the active text buffer, or leaves it.
func (f *form) textKey(st *qstate, k tea.KeyPressMsg) {
	switch k.Keystroke() {
	case "enter":
		st.free = false
		f.moveTab(1)
	case "down":
		// From the input line, down reaches the visible skip row.
		if st.textQuestion() && st.allowSkip {
			st.cursor = st.skipRow()
		}
	case "backspace":
		if len(st.text) > 0 {
			st.text = st.text[:len(st.text)-1]
		}
	default:
		if r := k.Keystroke(); len([]rune(r)) == 1 && r >= " " {
			st.text = append(st.text, []rune(r)...)
		}
	}
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

// pick selects option i by its number key.
func (f *form) pick(st *qstate, i int) {
	st.skipped = false
	if st.q.Kind == "multi" {
		f.toggle(st, i)
		return
	}
	st.sel = map[string]bool{st.opts[i].ID: true}
	f.moveTab(1)
}

// toggle flips a multi-select option.
func (f *form) toggle(st *qstate, i int) {
	if i >= len(st.opts) {
		return
	}
	id := st.opts[i].ID
	st.sel[id] = !st.sel[id]
	st.skipped = false
}

// enter activates the highlighted row or advances a text question.
func (f *form) enter(st *qstate) {
	switch {
	case st.onSkip():
		f.skip(st)
	case st.textQuestion():
		f.moveTab(1)
	case st.onOther():
		st.free = true
	case st.q.Kind == "multi":
		f.moveTab(1)
	case st.cursor < len(st.opts):
		f.pick(st, st.cursor)
	}
}

// skip marks the question unanswered and moves on.
func (f *form) skip(st *qstate) {
	st.skipped = true
	st.sel = map[string]bool{}
	st.text = nil
	st.free = false
	f.moveTab(1)
}

// typeRune starts free-text entry from anywhere on a choice question.
func (f *form) typeRune(st *qstate, r rune) {
	switch {
	case st.textQuestion():
		st.cursor = 0
		st.text = append(st.text, r)
	case st.other >= 0:
		st.cursor = st.other
		st.free = true
		st.skipped = false
		st.text = append(st.text, r)
	}
}

// Paste inserts text into the active text or note buffer, verbatim.
func (f *form) Paste(text string) {
	st := f.current()
	if st == nil {
		return
	}
	switch {
	case st.noteMode:
		st.note = append(st.note, []rune(text)...)
	case st.textQuestion():
		st.cursor = 0
		st.text = append(st.text, []rune(text)...)
	case st.other >= 0:
		st.cursor = st.other
		st.free = true
		st.text = append(st.text, []rune(text)...)
	}
}

// Tick does nothing for a form.
func (f *form) Tick(time.Time) bool { return false }

// answers builds the answers in question order.
func (f *form) answers(cancelled bool) []Answer {
	out := make([]Answer, 0, len(f.states))
	for _, st := range f.states {
		a := Answer{ID: st.q.ID, Note: string(st.note), Skipped: cancelled || st.skipped}
		text := string(st.text)
		switch {
		case a.Skipped:
		case text != "":
			a.Text = text
		case st.textQuestion():
			a.Skipped = true
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

// Size returns the full screen width and exactly the rows the content and hint
// need, so Compose can grow the sheet upward from the bottom.
func (f *form) Size(maxW, maxH int) (int, int) {
	if maxW <= 0 || maxH <= 0 {
		return max(maxW, 0), max(maxH, 0)
	}
	rows := len(f.body(maxW, styles.Styles{})) + len(f.hintLines(maxW, styles.Styles{}))
	return maxW, min(max(rows, 1), maxH)
}

// View renders the form full width, with the hint kept whole at the bottom.
func (f *form) View(w, h int, st styles.Styles) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	body := f.body(w, st)
	hint := f.hintLines(w, st)
	if len(hint) >= h {
		// A screen this short still gets the keys, which are the way out.
		body = nil
		hint = hint[:h]
	} else if room := h - len(hint); len(body) > room {
		body = body[:room]
		if room > 0 {
			body[room-1] = st.Dim.Render("… more")
		}
	}
	lines := append(body, hint...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = padTo(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

// body is the form above the hint, at width w.
func (f *form) body(w int, st styles.Styles) []string {
	var out []string
	out = append(out, st.Spark.Bold(true).Render(plain(f.title)))
	out = append(out, f.tabsLine(w, st), "")
	if f.current() == nil {
		out = append(out, f.review(w, st)...)
		return append(out, "")
	}
	q := f.current()
	for _, l := range wrapHard(plain(q.q.Prompt), w) {
		out = append(out, st.Bold.Render(l))
	}
	out = append(out, "")
	options := f.optionsLines(q, w, st)
	switch preview := f.preview(); {
	case preview != "" && w >= previewBreak:
		out = append(out, f.sideBySide(options, preview, w, st)...)
	case preview != "":
		out = append(out, options...)
		out = append(out, "", st.Muted.Render("preview:"))
		for _, l := range previewLines(preview, w) {
			out = append(out, st.Dim.Render(l))
		}
	default:
		out = append(out, options...)
	}
	out = append(out, "")
	return append(out, f.noteLines(q, w, st)...)
}

// tabsLine renders the one-row tab strip, shortening or windowing it to fit.
func (f *form) tabsLine(w int, st styles.Styles) string {
	entries := f.tabEntries(st, true)
	if joinWidth(entries) > w {
		entries = f.tabEntries(st, false)
	}
	if joinWidth(entries) <= w {
		return joinTabs(entries)
	}
	return windowTabs(entries, f.tab, w)
}

// tabEntries builds one entry per question plus the Submit tab. Long labels
// give way to Q1..Qn so the strip still says what is answered.
func (f *form) tabEntries(st styles.Styles, long bool) []string {
	out := make([]string, 0, len(f.states)+1)
	for i, qs := range f.states {
		label := "Q" + strconv.Itoa(i+1)
		if long {
			if id := strings.TrimSpace(plain(qs.q.ID)); id != "" {
				label = id
			}
		}
		label = ansi.Truncate(label, 14, "…")
		glyph, style := "☐", st.Muted
		switch {
		case qs.skipped:
			glyph, style = "⊘", st.Muted
		case answered(qs):
			glyph, style = "☑", st.Success
		}
		entry := glyph + " " + label
		if i == f.tab {
			out = append(out, st.Spark.Bold(true).Render(entry))
			continue
		}
		out = append(out, style.Render(entry))
	}
	submit := "✓ Submit →"
	if f.tab == len(f.states) {
		out = append(out, st.Spark.Bold(true).Render(submit))
	} else {
		out = append(out, st.Success.Render("✓")+" "+st.Muted.Render("Submit →"))
	}
	return out
}

// answered reports whether a question holds a non-skipped answer.
func answered(st *qstate) bool {
	if st.skipped {
		return false
	}
	if string(st.text) != "" {
		return true
	}
	for _, on := range st.sel {
		if on {
			return true
		}
	}
	return false
}

// joinWidth is the width of a joined tab strip.
func joinWidth(entries []string) int {
	n := 0
	for i, e := range entries {
		if i > 0 {
			n += 2
		}
		n += ansi.StringWidth(e)
	}
	return n
}

// joinTabs joins a tab strip.
func joinTabs(entries []string) string { return strings.Join(entries, "  ") }

// windowTabs keeps the active tab visible when the whole strip does not fit.
func windowTabs(entries []string, active, w int) string {
	if active < 0 || active >= len(entries) {
		return ""
	}
	lo, hi := active, active+1
	used := ansi.StringWidth(entries[active])
	for {
		grew := false
		if hi < len(entries) {
			if add := 2 + ansi.StringWidth(entries[hi]); used+add+2 <= w {
				used += add
				hi++
				grew = true
			}
		}
		if lo > 0 {
			if add := 2 + ansi.StringWidth(entries[lo-1]); used+add+2 <= w {
				used += add
				lo--
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	parts := make([]string, 0, hi-lo+2)
	if lo > 0 {
		parts = append(parts, "◀")
	}
	parts = append(parts, entries[lo:hi]...)
	if hi < len(entries) {
		parts = append(parts, "▶")
	}
	return joinTabs(parts)
}

// optionsLines renders the current question's numbered rows.
func (f *form) optionsLines(q *qstate, w int, st styles.Styles) []string {
	var out []string
	if q.textQuestion() {
		out = append(out, f.textLine(q, w, st))
	} else {
		for i, o := range q.opts {
			out = append(out, optionBlock(q, i, plain(o.Label), plain(o.Description), w, st)...)
		}
		if q.other >= 0 {
			label := "Type something."
			if text := plain(string(q.text)); text != "" {
				label = text
			}
			if q.free && q.onOther() {
				label += "▏"
			}
			out = append(out, optionBlock(q, q.other, label, "", w, st)...)
		}
	}
	if row := q.skipRow(); row >= 0 {
		marker := "  "
		if q.cursor == row {
			marker = st.Spark.Render("> ")
		}
		out = append(out, marker+st.Muted.Render("Skip this question"))
	}
	return out
}

// textLine renders a free-text answer with its cursor.
func (f *form) textLine(q *qstate, w int, st styles.Styles) string {
	switch {
	case q.cursor != 0:
		return "  " + st.Dim.Render("Type your answer…")
	case string(q.text) == "":
		return st.Spark.Render("> ") + st.Dim.Render("Type your answer…") + "▏"
	default:
		return st.Spark.Render("> ") + st.Text.Render(plain(string(q.text))) + "▏"
	}
}

// optionBlock renders one numbered option: a bold label and a dim description
// on its own indented line.
func optionBlock(q *qstate, i int, label, desc string, w int, st styles.Styles) []string {
	marker := "  "
	if i == q.cursor {
		marker = st.Spark.Render("> ")
	}
	check := ""
	if q.q.Kind == "multi" {
		switch {
		case i < len(q.opts) && q.sel[q.opts[i].ID]:
			check = st.Success.Render("[x] ")
		default:
			check = st.Muted.Render("[ ] ")
		}
	}
	num := st.Muted.Render(strconv.Itoa(i+1) + ". ")
	indent := 2 + ansi.StringWidth(check) + ansi.StringWidth(num)
	base, rec := recommended(label)
	recW := 0
	if rec != "" {
		recW = ansi.StringWidth(rec) + 1
	}
	lines := wrapHard(base, max(w-indent-recW, 1))
	out := make([]string, 0, len(lines)+1)
	for j, l := range lines {
		line := ""
		if j == 0 {
			line = marker + check + num
		} else {
			line = strings.Repeat(" ", indent)
		}
		line += st.Bold.Render(l)
		if j == len(lines)-1 && rec != "" {
			line += " " + st.Success.Render(rec)
		}
		out = append(out, line)
	}
	if desc != "" {
		for _, l := range wrapHard(desc, max(w-indent, 1)) {
			out = append(out, strings.Repeat(" ", indent)+st.Dim.Render(l))
		}
	}
	return out
}

// recommended splits a trailing "(Recommended)" off a label.
func recommended(label string) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(label))
	if !strings.HasSuffix(lower, "(recommended)") {
		return strings.TrimSpace(label), ""
	}
	cut := len(label) - len("(recommended)")
	return strings.TrimSpace(label[:cut]), strings.TrimSpace(label[cut:])
}

// noteLines renders the note affordance where the note will appear.
func (f *form) noteLines(q *qstate, w int, st styles.Styles) []string {
	const prefix = "Notes: "
	note := plain(string(q.note))
	switch {
	case q.noteMode:
		return labelWrap(prefix, note+"▏", w, st.Warm)
	case note != "":
		return labelWrap(prefix, note, w, st.Warm)
	default:
		return []string{st.Muted.Render(prefix + "ctrl+n to add a note")}
	}
}

// labelWrap wraps text under a short prefix, keeping the prefix on the first
// line and indenting the continuation to match it.
func labelWrap(prefix, text string, w int, style lipgloss.Style) []string {
	indent := strings.Repeat(" ", ansi.StringWidth(prefix))
	lines := wrapHard(text, max(w-ansi.StringWidth(prefix), 1))
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if i == 0 {
			out = append(out, style.Render(prefix+l))
			continue
		}
		out = append(out, style.Render(indent+l))
	}
	return out
}

// preview returns the highlighted option's preview text.
func (f *form) preview() string {
	q := f.current()
	if q == nil || q.editing() || q.cursor >= len(q.opts) {
		return ""
	}
	return q.opts[q.cursor].Preview
}

// sideBySide puts the highlighted option's preview in a right-hand column,
// clipping both columns with an ellipsis.
func (f *form) sideBySide(left []string, preview string, w int, st styles.Styles) []string {
	leftW := 0
	for _, l := range left {
		leftW = max(leftW, ansi.StringWidth(l))
	}
	leftW = min(leftW, max(w*45/100, 20))
	leftW = min(leftW, w-4)
	rightW := max(w-leftW-3, 1)
	right := previewLines(preview, rightW)
	out := make([]string, 0, max(len(left), len(right)))
	for i := 0; i < max(len(left), len(right)); i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, padTo(l, leftW)+st.Muted.Render(" │ ")+padTo(r, rightW))
	}
	return out
}

// previewLines wraps a preview to width, one source line at a time.
func previewLines(preview string, w int) []string {
	var out []string
	block := strings.NewReplacer("\t", "    ").Replace(sanitize.Text(preview))
	for _, raw := range strings.Split(block, "\n") {
		out = append(out, wrapHard(raw, w)...)
	}
	return out
}

// hintLines wraps the key hints by whole items, never by cutting one.
func (f *form) hintLines(w int, st styles.Styles) []string {
	items := f.hintItems()
	var out []string
	cur := ""
	for _, it := range items {
		switch {
		case cur == "":
			cur = it
		case ansi.StringWidth(cur)+3+ansi.StringWidth(it) <= w:
			cur += " · " + it
		default:
			out = append(out, st.Dim.Render(cur))
			cur = it
		}
	}
	if cur != "" {
		out = append(out, st.Dim.Render(cur))
	}
	return out
}

// hintItems lists the keys that work right now.
func (f *form) hintItems() []string {
	q := f.current()
	switch {
	case q == nil:
		return []string{"Enter submit", "Tab switch", "Esc cancel"}
	case q.noteMode:
		return []string{"Enter save note", "Esc done"}
	case q.editing():
		return []string{"type to answer", "Enter next", "ctrl+n note", "Tab switch", "Esc cancel"}
	}
	items := []string{"↑↓ move"}
	if q.q.Kind == "multi" {
		items = append(items, "Space toggle")
	}
	if len(q.opts) > 0 {
		items = append(items, "1-9 pick")
	}
	if q.other >= 0 {
		items = append(items, "type for other")
	}
	return append(items, "Enter next", "ctrl+n note", "Tab switch", "Esc cancel")
}

// review renders every answer.
func (f *form) review(w int, st styles.Styles) []string {
	var out []string
	for i, a := range f.answers(false) {
		for _, l := range wrapHard("Q"+strconv.Itoa(i+1)+": "+plain(f.states[i].q.Prompt), w) {
			out = append(out, st.Bold.Render(l))
		}
		out = append(out, "  "+st.Muted.Render(plain(f.answerText(i, a))))
		if a.Note != "" {
			out = append(out, "  "+st.Warm.Render("note: "+plain(a.Note)))
		}
		out = append(out, "")
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

// plain makes untrusted text safe to draw on one or more lines.
func plain(s string) string {
	return strings.NewReplacer("\t", "    ", "\n", " ").Replace(sanitize.Text(s))
}

// wrapHard wraps text to width, truncating an unbreakable token with an
// ellipsis rather than letting it overflow.
func wrapHard(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	wrapped := strings.Split(ansi.Wordwrap(s, w, ""), "\n")
	for i := range wrapped {
		wrapped[i] = ansi.Truncate(wrapped[i], w, "…")
	}
	return wrapped
}

// padTo clips a line to width with an ellipsis and pads it to exactly width.
func padTo(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}
