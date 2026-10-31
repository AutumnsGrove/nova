package dialog

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

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
