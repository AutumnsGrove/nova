//go:build !notui

package dialog

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/styles"
)

func q(id, prompt, kind string, options ...proto.UIOption) proto.UIQuestion {
	return proto.UIQuestion{ID: id, Prompt: prompt, Kind: kind, Options: options}
}

func opt(id, label string) proto.UIOption { return proto.UIOption{ID: id, Label: label} }

func formReq(qs ...proto.UIQuestion) proto.UIRequest {
	return proto.UIRequest{Type: proto.FrameUIRequest, ID: "f", Title: "Questions", Questions: qs, AllowSkip: true}
}

// submit navigates to the Review tab and submits.
func submit(t *testing.T, d Dialog) []Answer {
	t.Helper()
	f := d.(*form)
	for f.tab != len(f.states) {
		d.HandleKey(tabKey)
	}
	r := d.HandleKey(enterKey)
	if !r.Done {
		t.Fatal("form never submitted")
	}
	answers, ok := r.Value.([]Answer)
	if !ok {
		t.Fatalf("value = %#v", r.Value)
	}
	return answers
}

func TestForm_FRD01_Tabs(t *testing.T) {
	d := NewForm(formReq(q("a", "A", "single", opt("x", "X")), q("b", "B", "text"), q("c", "C", "text")))
	if got := d.(*form).tabs(); got != 4 {
		t.Fatalf("tabs = %d, want 4", got)
	}
}

func TestForm_FRD02_Single(t *testing.T) {
	d := NewForm(formReq(q("a", "Pick", "single", opt("x", "X"), opt("y", "Y"))))
	d.HandleKey(enterKey)
	answers := submit(t, d)
	if len(answers) != 1 || len(answers[0].IDs) != 1 || answers[0].IDs[0] != "x" {
		t.Fatalf("answers = %+v", answers)
	}
}

func TestForm_FRD03_Multi(t *testing.T) {
	d := NewForm(formReq(q("a", "Pick", "multi", opt("x", "X"), opt("y", "Y"), opt("z", "Z"))))
	d.HandleKey(spaceKey)
	d.HandleKey(downKey)
	d.HandleKey(spaceKey)
	d.HandleKey(enterKey)
	answers := submit(t, d)
	if len(answers[0].IDs) != 2 || answers[0].IDs[0] != "x" || answers[0].IDs[1] != "y" {
		t.Fatalf("answers = %+v", answers)
	}
}

func TestForm_FRD04_FreeText(t *testing.T) {
	d := NewForm(formReq(q("a", "Say", "text")))
	for _, r := range "hello" {
		d.HandleKey(runeKey(r))
	}
	answers := submit(t, d)
	if len(answers) != 1 || answers[0].Text != "hello" {
		t.Fatalf("answers = %+v", answers)
	}
}

func TestForm_FRD05_Other(t *testing.T) {
	d := NewForm(formReq(q("a", "Pick", "single", opt("x", "X"))))
	d.HandleKey(downKey) // to Other
	d.HandleKey(enterKey)
	for _, r := range "custom" {
		d.HandleKey(runeKey(r))
	}
	answers := submit(t, d)
	if len(answers[0].IDs) != 0 || answers[0].Text != "custom" {
		t.Fatalf("answers = %+v", answers)
	}
}

func TestForm_FRD06_Note(t *testing.T) {
	d := NewForm(formReq(q("a", "Pick", "single", opt("x", "X"))))
	d.HandleKey(ctrlNKey)
	note := "line1\nline2 日本語"
	d.(Paster).Paste(note)
	d.HandleKey(enterKey) // commit note
	d.HandleKey(enterKey) // select X and move on
	answers := submit(t, d)
	if answers[0].Note != note {
		t.Fatalf("note = %q, want %q", answers[0].Note, note)
	}
}

func TestForm_FRD07_Skip(t *testing.T) {
	d := NewForm(formReq(q("a", "Pick", "single", opt("x", "X"))))
	d.HandleKey(downKey) // Other
	d.HandleKey(downKey) // Skip this question
	d.HandleKey(enterKey)
	answers := submit(t, d)
	if !answers[0].Skipped {
		t.Fatalf("answers = %+v", answers)
	}
}

func TestForm_FRD08_Partial(t *testing.T) {
	d := NewForm(formReq(q("a", "A", "single", opt("x", "X")), q("b", "B", "single", opt("y", "Y"))))
	d.HandleKey(enterKey) // answer Q1
	answers := submit(t, d)
	if answers[0].Skipped || len(answers[0].IDs) != 1 {
		t.Fatalf("Q1 = %+v", answers[0])
	}
	if !answers[1].Skipped {
		t.Fatalf("Q2 = %+v, want skipped", answers[1])
	}
}

func TestForm_FRD09_Review(t *testing.T) {
	d := NewForm(formReq(q("a", "First?", "single", opt("x", "X")), q("b", "Second?", "text")))
	d.HandleKey(ctrlNKey)
	d.(Paster).Paste("a note")
	d.HandleKey(enterKey)
	d.HandleKey(enterKey)
	for _, r := range "text" {
		d.HandleKey(runeKey(r))
	}
	d.HandleKey(enterKey)
	// Now on Review.
	v := d.View(80, 30, styles.Styles{})
	for _, want := range []string{"First?", "Second?", "X", "text", "a note"} {
		if !strings.Contains(v, want) {
			t.Fatalf("review missing %q:\n%s", want, v)
		}
	}
}

func TestForm_FRD10_EscCancels(t *testing.T) {
	d := NewForm(formReq(q("a", "A", "single", opt("x", "X")), q("b", "B", "text")))
	r := d.HandleKey(escKey)
	if !r.Done || !r.Cancelled {
		t.Fatalf("result = %+v", r)
	}
	answers := r.Value.([]Answer)
	if len(answers) != 2 || !answers[0].Skipped || !answers[1].Skipped {
		t.Fatalf("answers = %+v", answers)
	}
}

func TestForm_FRD11_NavigationWraps(t *testing.T) {
	d := NewForm(formReq(q("a", "A", "single", opt("x", "X")), q("b", "B", "single", opt("y", "Y"))))
	f := d.(*form)
	d.HandleKey(tabKey)
	d.HandleKey(tabKey)
	if f.tab != f.tabs()-1 {
		t.Fatalf("tab = %d, want Review", f.tab)
	}
	d.HandleKey(tabKey)
	if f.tab != 0 {
		t.Fatalf("tab did not wrap to Q1: %d", f.tab)
	}
	d.HandleKey(shiftTabKey)
	if f.tab != f.tabs()-1 {
		t.Fatalf("shift+tab did not wrap to Review: %d", f.tab)
	}
}

func TestForm_FRD12_Preview(t *testing.T) {
	opts := []proto.UIOption{{ID: "x", Label: "X", Preview: "PREVIEW-BODY"}}
	d := NewForm(formReq(q("a", "Pick", "single", opts...)))
	wide := d.View(120, 20, styles.Styles{})
	narrow := d.View(60, 20, styles.Styles{})
	if !sameLine(wide, "X", "PREVIEW-BODY") {
		t.Fatalf("wide preview not side by side:\n%s", wide)
	}
	if sameLine(narrow, "X", "PREVIEW-BODY") {
		t.Fatalf("narrow preview should be below:\n%s", narrow)
	}
	if !strings.Contains(narrow, "PREVIEW-BODY") {
		t.Fatalf("narrow preview missing:\n%s", narrow)
	}
}

// sameLine reports whether a and b appear on one line.
func sameLine(s, a, b string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, a) && strings.Contains(l, b) {
			return true
		}
	}
	return false
}

func TestForm_FRD13_Limits(t *testing.T) {
	var qs []proto.UIQuestion
	many := make([]proto.UIOption, 12)
	for i := range many {
		many[i] = opt(string(rune('a'+i)), "O")
	}
	for i := 0; i < 12; i++ {
		qs = append(qs, q("q"+string(rune('a'+i)), "Q", "single", many...))
	}
	f := NewForm(formReq(qs...)).(*form)
	if len(f.states) != maxQuestions {
		t.Fatalf("states = %d, want %d", len(f.states), maxQuestions)
	}
	if len(f.states[0].opts) != maxOptions {
		t.Fatalf("opts = %d, want %d", len(f.states[0].opts), maxOptions)
	}
}

func TestForm_FRD14_NoOptionsBecomesText(t *testing.T) {
	f := NewForm(formReq(q("a", "Say", "single"))).(*form)
	if !f.states[0].free {
		t.Fatal("a single question with no options should be free text")
	}
	d := f
	for _, r := range "typed" {
		d.HandleKey(runeKey(r))
	}
	answers := submit(t, d)
	if answers[0].Text != "typed" {
		t.Fatalf("answers = %+v", answers)
	}
}

func TestForm_FRD15_Order(t *testing.T) {
	d := NewForm(formReq(
		q("first", "A", "single", opt("x", "X")),
		q("second", "B", "single", opt("y", "Y")),
		q("third", "C", "single", opt("z", "Z")),
	))
	d.HandleKey(enterKey)
	d.HandleKey(enterKey)
	d.HandleKey(enterKey)
	answers := submit(t, d)
	if answers[0].ID != "first" || answers[1].ID != "second" || answers[2].ID != "third" {
		t.Fatalf("order = %+v", answers)
	}
}

func TestForm_FRD16_LongText(t *testing.T) {
	long := strings.Repeat("word ", 40)
	d := NewForm(formReq(q("a", long, "single", opt("x", long))))
	for _, w := range []int{40, 80, 120} {
		for _, l := range strings.Split(d.View(w, 30, styles.Styles{}), "\n") {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: line %d wide %q", w, ansi.StringWidth(l), l)
			}
		}
	}
}

func TestForm_FRD17_Golden(t *testing.T) {
	d := NewForm(formReq(
		q("a", "Which approach?", "single", proto.UIOption{ID: "x", Label: "Fix now", Description: "one line"}, proto.UIOption{ID: "y", Label: "Defer"}),
		q("b", "Notes", "text"),
	))
	for _, w := range []int{50, 80, 120} {
		t.Run(strconv.Itoa(w), func(t *testing.T) {
			_, h := d.Size(w, 30)
			golden.RequireEqual(t, d.View(w, h, styles.Styles{}))
		})
	}
}

// content is the form at the height its own Size asks for.
func content(t *testing.T, d Dialog, w int) (string, []string) {
	t.Helper()
	_, h := d.Size(w, 40)
	out := d.View(w, h, styles.Styles{})
	return out, strings.Split(out, "\n")
}

func TestForm_FRD19_ContentHeight(t *testing.T) {
	d := NewForm(formReq(
		q("a", "Which approach?", "single", opt("x", "Fix now"), opt("y", "Defer")),
		q("b", "Notes", "text"),
		q("c", "Else?", "single", opt("z", "Z")),
	))
	w, h := d.Size(100, 40)
	if w != 100 {
		t.Fatalf("width = %d, want full width", w)
	}
	if h >= 40 {
		t.Fatalf("height = %d, want content height under 40", h)
	}
	out, lines := content(t, d, 100)
	if len(lines) != h {
		t.Fatalf("View has %d lines, Size said %d", len(lines), h)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" && strings.TrimSpace(lines[i-1]) == "" {
			t.Fatalf("blank filler at line %d:\n%s", i, out)
		}
	}
	if !strings.Contains(lines[len(lines)-1], "Esc cancel") {
		t.Fatalf("last line is not the hint:\n%s", out)
	}
}

func TestForm_FRD20_WidthInvariant(t *testing.T) {
	long := strings.Repeat("a description that keeps going ", 8)
	big := proto.UIOption{ID: "x", Label: "An option label that is far too long to fit on one narrow line", Description: long, Preview: strings.Repeat("preview words ", 20)}
	for _, w := range []int{50, 80, 120} {
		for _, preview := range []bool{false, true} {
			o := big
			if !preview {
				o.Preview = ""
			}
			d := NewForm(formReq(q("a", "A prompt that is also long enough to wrap at some widths", "single", o)))
			_, lines := content(t, d, w)
			for i, l := range lines {
				if got := ansi.StringWidth(l); got != w {
					t.Fatalf("w=%d preview=%v line %d width = %d: %q", w, preview, i, got, l)
				}
			}
		}
	}
}

func TestForm_FRD21_MultiCursorAndCheckbox(t *testing.T) {
	d := NewForm(formReq(q("a", "Pick", "multi", opt("x", "X"), opt("y", "Y"))))
	_, before := content(t, d, 80)
	d.HandleKey(spaceKey) // select X
	d.HandleKey(downKey)  // cursor to Y
	_, after := content(t, d, 80)
	if strings.Join(before, "\n") == strings.Join(after, "\n") {
		t.Fatal("moving the cursor on a multi-select question changed nothing")
	}
	d.HandleKey(spaceKey) // select Y too
	d.HandleKey(upKey)    // cursor back to X
	out, _ := content(t, d, 80)
	row := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "[x]") && strings.Contains(l, ">") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("no row shows both the cursor and [x]:\n%s", out)
	}
}

func TestForm_FRD22_Hotkeys(t *testing.T) {
	d := NewForm(formReq(q("a", "Pick", "single", opt("x", "X"), opt("y", "Y")), q("b", "B", "text")))
	d.HandleKey(runeKey('2'))
	if f := d.(*form); f.tab != 1 {
		t.Fatalf("a number key did not pick and advance: tab %d", f.tab)
	}
	answers := submit(t, d)
	if len(answers[0].IDs) != 1 || answers[0].IDs[0] != "y" {
		t.Fatalf("answers = %+v", answers)
	}

	m := NewForm(formReq(q("a", "Pick", "multi", opt("x", "X"), opt("y", "Y"))))
	m.HandleKey(runeKey('2'))
	if f := m.(*form); f.tab != 0 {
		t.Fatalf("a number key left the multi-select tab: %d", f.tab)
	}
	if got := submit(t, m); len(got[0].IDs) != 1 || got[0].IDs[0] != "y" {
		t.Fatalf("multi answers = %+v", got)
	}
}

func TestForm_FRD23_Ellipsis(t *testing.T) {
	o := proto.UIOption{ID: "x", Label: "X", Description: strings.Repeat("z", 200)}
	d := NewForm(formReq(q("a", "Pick", "single", o)))
	out, lines := content(t, d, 50)
	if !strings.Contains(out, "…") {
		t.Fatalf("an unbreakable description was clipped with no ellipsis:\n%s", out)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 50 {
			t.Fatalf("line over width: %q", l)
		}
	}
}

func TestForm_FRD24_HintIsWhole(t *testing.T) {
	d := NewForm(formReq(q("a", "Pick", "single", opt("x", "X"), opt("y", "Y"))))
	out, lines := content(t, d, 50)
	// The hint is the final run of lines, after the last blank one.
	start := 0
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			start = i + 1
		}
	}
	hint := strings.Join(lines[start:], " ")
	for _, want := range []string{"↑↓ move", "1-9 pick", "ctrl+n note", "Tab switch", "Esc cancel"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint at width 50 is missing %q:\n%s", want, out)
		}
	}
	for _, l := range lines[start:] {
		if ansi.StringWidth(l) > 50 {
			t.Fatalf("hint line over width: %q", l)
		}
	}
}

func TestForm_FRD25_SkipIsNotALetter(t *testing.T) {
	no := false
	req := formReq(q("a", "Pick", "single", opt("x", "X")))
	req.Questions[0].AllowOther = &no
	d := NewForm(req)
	d.HandleKey(runeKey('s'))
	f := d.(*form)
	if f.states[0].skipped {
		t.Fatal("s still skips the question")
	}
	if got := string(f.states[0].text); got != "" {
		t.Fatalf("s went into a buffer on a question with no free-text row: %q", got)
	}
	d.HandleKey(downKey) // Skip this question is the only row below X
	d.HandleKey(enterKey)
	if !f.states[0].skipped {
		t.Fatal("the visible skip row did not skip")
	}

	text := NewForm(formReq(q("b", "Say", "text")))
	text.HandleKey(runeKey('s'))
	if got := string(text.(*form).states[0].text); got != "s" {
		t.Fatalf("s on a text question = %q, want the typed letter", got)
	}
}

func TestForm_FRD26_TabStates(t *testing.T) {
	d := NewForm(formReq(
		q("Ambiguity", "A", "single", opt("x", "X")),
		q("Explanations", "B", "single", opt("y", "Y")),
		q("Off-topic", "C", "single", opt("z", "Z")),
	))
	d.HandleKey(enterKey) // answer Q1
	d.HandleKey(downKey)  // Other
	d.HandleKey(downKey)  // Skip
	d.HandleKey(enterKey) // skip Q2
	out, lines := content(t, d, 100)
	tab := lines[1]
	for _, want := range []string{"☑ Ambiguity", "⊘ Explanations", "☐ Off-topic", "Submit"} {
		if !strings.Contains(tab, want) {
			t.Fatalf("tab strip is missing %q:\n%s", want, out)
		}
	}
	// Submit is reachable by tabbing and enters there.
	for d.(*form).tab != len(d.(*form).states) {
		d.HandleKey(tabKey)
	}
	if r := d.HandleKey(enterKey); !r.Done || r.Cancelled {
		t.Fatalf("Submit did not submit: %+v", r)
	}
}

func TestForm_FRD27_NoteKey(t *testing.T) {
	d := NewForm(formReq(q("a", "Say", "text")))
	d.HandleKey(runeKey('n'))
	d.HandleKey(ctrlNKey)
	d.(Paster).Paste("a note")
	d.HandleKey(enterKey) // save the note
	answers := submit(t, d)
	if answers[0].Text != "n" {
		t.Fatalf("text = %q, want the typed n", answers[0].Text)
	}
	if answers[0].Note != "a note" {
		t.Fatalf("note = %q", answers[0].Note)
	}
}

func TestForm_FRD28_SkipRowOnText(t *testing.T) {
	d := NewForm(formReq(q("a", "Say", "text")))
	out, _ := content(t, d, 80)
	if !strings.Contains(out, "Skip this question") {
		t.Fatalf("a text question has no visible skip row:\n%s", out)
	}
	d.HandleKey(downKey)
	d.HandleKey(enterKey)
	answers := submit(t, d)
	if !answers[0].Skipped || answers[0].Text != "" {
		t.Fatalf("answers = %+v", answers)
	}
}

func TestForm_FRD29_Recommended(t *testing.T) {
	st := styles.Build(styles.Dark(), colorprofile.TrueColor)
	d := NewForm(formReq(q("a", "Pick", "single", proto.UIOption{ID: "x", Label: "Stop and ask (Recommended)"})))
	out := d.View(100, 30, st)
	if !strings.Contains(out, "(Recommended)") {
		t.Fatalf("the recommendation is gone:\n%s", out)
	}
	if !strings.Contains(out, "38;2;126;224;161") {
		t.Fatalf("(Recommended) is not in the success colour:\n%q", out)
	}
}

func TestForm_FRD30_TabStripWindows(t *testing.T) {
	var qs []proto.UIQuestion
	for i := 0; i < maxQuestions; i++ {
		qs = append(qs, q("question-"+strconv.Itoa(i+1), "Q", "single", opt("x", "X")))
	}
	d := NewForm(formReq(qs...))
	for i := 0; i < maxQuestions-1; i++ {
		d.HandleKey(tabKey)
	}
	out, lines := content(t, d, 50)
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != 50 {
			t.Fatalf("line %d width = %d: %q", i, got, l)
		}
	}
	tab := lines[1]
	if !strings.Contains(tab, "◀") {
		t.Fatalf("the strip hides earlier questions with no marker:\n%s", out)
	}
	if !strings.Contains(tab, "Q8") || !strings.Contains(tab, "Submit") {
		t.Fatalf("the active tab or Submit is not visible:\n%s", out)
	}
}
