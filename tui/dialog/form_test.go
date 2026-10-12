//go:build !notui

package dialog

import (
	"strings"
	"testing"

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
	d.HandleKey(runeKey('n'))
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
	d.HandleKey(runeKey('s'))
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
	d.HandleKey(runeKey('n'))
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
	for _, tc := range []struct {
		name string
		w, h int
	}{{"narrow", 50, 18}, {"mid", 80, 20}, {"wide", 120, 22}} {
		t.Run(tc.name, func(t *testing.T) {
			golden.RequireEqual(t, d.View(tc.w, tc.h, styles.Styles{}))
		})
	}
}
