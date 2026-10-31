package dialog

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/styles"
)

func truecolor() styles.Styles { return styles.Build(styles.Dark(), colorprofile.TrueColor) }

// A dialog must not flatten the styling of the transcript behind it.
func TestDialog_DLG11_ComposeKeepsBaseStyle(t *testing.T) {
	st := truecolor()
	base := st.Bold.Render("heading") + " plain text"
	o := NewOverlay()
	o.Push(NewConfirm(confirmReq("x", "Allow write?", "because", nil)))
	out := o.Compose(base, 60, 14, st)
	lines := strings.Split(out, "\n")
	// The confirm box is six rows in a fourteen-row screen, so row zero is base.
	if !strings.Contains(lines[0], "\x1b[") {
		t.Fatalf("base styling was stripped:\n%q", lines[0])
	}
	if !strings.Contains(lines[0], "heading") {
		t.Fatalf("base text missing:\n%q", lines[0])
	}
}

// A framed dialog must draw the styling its body already carries.
func TestDialog_DLG12_FrameKeepsStyle(t *testing.T) {
	st := truecolor()
	out := frame("T", []string{st.Bold.Render("bold") + " " + st.Dim.Render("dim")}, "", 40, 5, st)
	if !strings.Contains(out, "\x1b[1;") || !strings.Contains(out, "\x1b[2;") {
		t.Fatalf("frame stripped the body styles:\n%q", out)
	}
}

// The form's own styling must reach the screen.
func TestForm_FRD18_StyleSurvivesTheFrame(t *testing.T) {
	st := truecolor()
	d := NewForm(formReq(q("a", "Which approach?", "single", opt("x", "Fix now"))))
	out := d.View(100, 30, st)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("the form rendered with no SGR at all:\n%s", out)
	}
}

var _ = proto.FrameUIRequest

// The form is a bottom sheet: full width, content height, no box.
func TestDialog_DLG13_SheetPlacement(t *testing.T) {
	st := truecolor()
	base := st.Bold.Render("transcript stays here") + "\nsecond line"
	o := NewOverlay()
	d := NewForm(formReq(
		q("Ambiguity", "Which approach?", "single", opt("x", "Fix now"), opt("y", "Defer")),
		q("Explanations", "How much?", "text"),
	))
	o.Push(d)
	out := o.Compose(base, 60, 24, st)
	lines := strings.Split(out, "\n")
	if len(lines) != 24 {
		t.Fatalf("lines = %d, want 24", len(lines))
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != 60 {
			t.Fatalf("line %d width = %d: %q", i, got, l)
		}
	}
	if strings.Contains(out, "┌") || strings.Contains(out, "└") {
		t.Fatalf("the sheet drew a box:\n%s", out)
	}
	_, bh := d.Size(60, 24)
	if bh >= 24 {
		t.Fatalf("sheet height = %d, want content height", bh)
	}
	if !strings.Contains(lines[24-bh], "Questions") {
		t.Fatalf("the sheet is not anchored to the bottom:\n%s", out)
	}
	if !strings.Contains(lines[0], "transcript stays here") {
		t.Fatalf("the transcript above the sheet was lost:\n%s", out)
	}
}
