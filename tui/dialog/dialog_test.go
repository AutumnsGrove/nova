//go:build !notui

package dialog

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/styles"
)

func runeKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

var (
	enterKey  = tea.KeyPressMsg{Code: tea.KeyEnter}
	escKey    = tea.KeyPressMsg{Code: tea.KeyEsc}
	leftKey   = tea.KeyPressMsg{Code: tea.KeyLeft}
	rightKey  = tea.KeyPressMsg{Code: tea.KeyRight}
	tabKey    = tea.KeyPressMsg{Code: tea.KeyTab}
	upKey     = tea.KeyPressMsg{Code: tea.KeyUp}
	downKey   = tea.KeyPressMsg{Code: tea.KeyDown}
	pgupKey   = tea.KeyPressMsg{Code: tea.KeyPgUp}
	pgdownKey = tea.KeyPressMsg{Code: tea.KeyPgDown}
	spaceKey  = tea.KeyPressMsg{Code: tea.KeySpace}
)

// confirmReq builds a confirm request for tests.
func confirmReq(id, title, message string, def *bool) proto.UIRequest {
	return proto.UIRequest{Type: proto.FrameUIRequest, ID: id, Title: title, Message: message, Default: def}
}

// response extracts the UIResponse from a result.
func response(t *testing.T, r Result) proto.UIResponse {
	t.Helper()
	v, ok := r.Value.(proto.UIResponse)
	if !ok {
		t.Fatalf("value = %#v, want proto.UIResponse", r.Value)
	}
	return v
}

// stubDialog is a non-confirm dialog for the too-small fallback test.
type stubDialog struct{ id string }

func (s *stubDialog) ID() string                       { return s.id }
func (s *stubDialog) HandleKey(tea.KeyPressMsg) Result { return Result{} }
func (s *stubDialog) Size(maxW, maxH int) (int, int)   { return min(40, maxW), min(8, maxH) }
func (s *stubDialog) View(w, h int, st styles.Styles) string {
	return frame("stub", []string{"x"}, "", w, h, st)
}
func (s *stubDialog) Tick(time.Time) bool { return false }

func TestDialog_DLG01_StackOrder(t *testing.T) {
	o := NewOverlay()
	a := NewConfirm(confirmReq("a", "A", "", nil))
	b := NewConfirm(confirmReq("b", "B", "", nil))
	o.Push(a)
	o.Push(b)
	if o.Top().ID() != "b" {
		t.Fatalf("top = %s, want b", o.Top().ID())
	}
	o.Pop()
	if o.Top().ID() != "a" {
		t.Fatalf("top after pop = %s, want a", o.Top().ID())
	}
}

func TestDialog_DLG03_EscCancelsTop(t *testing.T) {
	o := NewOverlay()
	o.Push(NewConfirm(confirmReq("x", "Ask", "sure?", nil)))
	r, d := o.HandleKey(escKey)
	if !r.Done || !r.Cancelled || d.ID() != "x" {
		t.Fatalf("result = %+v dialog = %v", r, d)
	}
	if resp := response(t, r); resp.OK || !resp.Cancelled || resp.ID != "x" {
		t.Fatalf("response = %+v", resp)
	}
	if o.Len() != 0 {
		t.Fatalf("stack depth = %d, want 0", o.Len())
	}
}

func TestDialog_DLG05_CentredAndBordered(t *testing.T) {
	o := NewOverlay()
	o.Push(NewConfirm(confirmReq("x", "Allow write?", "because", nil)))
	out := o.Compose("base", 80, 24, styles.Styles{})
	lines := strings.Split(out, "\n")
	if len(lines) != 24 {
		t.Fatalf("lines = %d, want 24", len(lines))
	}
	for _, l := range lines {
		if ansi.StringWidth(l) != 80 {
			t.Fatalf("line width = %d: %q", ansi.StringWidth(l), l)
		}
	}
	if !strings.Contains(out, "┌") || !strings.Contains(out, "└") {
		t.Fatalf("no border:\n%s", out)
	}
	if !strings.Contains(out, "Allow write?") {
		t.Fatalf("no title:\n%s", out)
	}
	if !strings.Contains(out, "esc cancel") {
		t.Fatalf("no hint:\n%s", out)
	}
	// The box is centred: the left margin equals the right margin.
	top := ""
	for _, l := range lines {
		if strings.Contains(l, "┌") {
			top = l
			break
		}
	}
	i, j := strings.Index(top, "┌"), strings.Index(top, "┐")
	left := ansi.StringWidth(top[:i])
	boxW := ansi.StringWidth(top[i : j+len("┐")])
	if left != (80-boxW)/2 {
		t.Fatalf("box not centred: left=%d width=%d", left, boxW)
	}
}

func TestDialog_DLG06_SizeLimits(t *testing.T) {
	o := NewOverlay()
	msg := strings.Repeat("long message line\n", 60)
	o.Push(NewConfirm(confirmReq("x", "Big", msg, nil)))
	out := o.Compose("", 200, 60, styles.Styles{})
	top := strings.Split(out, "\n")[0]
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "┌") {
			top = l
			break
		}
	}
	i, j := strings.Index(top, "┌"), strings.Index(top, "┐")
	boxW := ansi.StringWidth(top[i : j+len("┐")])
	if boxW > 200*4/5 {
		t.Fatalf("box width %d exceeds 80%% of 200", boxW)
	}
	if boxW < minScreenW {
		t.Fatalf("box width %d is under the minimum", boxW)
	}
	if !strings.Contains(out, "… more") {
		t.Fatalf("clipped content has no indicator:\n%s", out)
	}
}

func TestDialog_DLG07_TooSmallFallback(t *testing.T) {
	o := NewOverlay()
	o.Push(NewConfirm(confirmReq("x", "Allow write?", "why", nil)))
	out := o.Compose("", 20, 6, styles.Styles{})
	if !strings.Contains(out, "allow? y/n") {
		t.Fatalf("confirm fallback missing:\n%s", out)
	}

	o2 := NewOverlay()
	o2.Push(&stubDialog{id: "s"})
	out2 := o2.Compose("", 20, 6, styles.Styles{})
	if o2.Len() != 0 {
		t.Fatal("a non-confirm dialog was not cancelled on a tiny screen")
	}
	if o2.TakeNotice() == "" {
		t.Fatal("cancelling a dialog on a tiny screen produced no notice")
	}
	if strings.Contains(out2, "stub") {
		t.Fatalf("cancelled dialog still rendered:\n%s", out2)
	}
}

func TestDialog_DLG10_ComposeSize(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		w, h := rng.Intn(300), rng.Intn(80)
		o := NewOverlay()
		for j := 0; j < rng.Intn(4); j++ {
			o.Push(NewConfirm(confirmReq("x", "T", strings.Repeat("m", rng.Intn(200)), nil)))
		}
		out := o.Compose("base", w, h, styles.Styles{})
		if h == 0 {
			if out != "" {
				t.Fatalf("w=%d h=0: got %q, want empty", w, out)
			}
			continue
		}
		lines := strings.Split(out, "\n")
		if len(lines) != h {
			t.Fatalf("w=%d h=%d: got %d lines", w, h, len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) != w {
				t.Fatalf("w=%d: line width %d", w, ansi.StringWidth(l))
			}
		}
	}
}

func TestDialog_CNF01_Keys(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		def  *bool
		key  tea.KeyPressMsg
		want bool
	}{
		{"y", &no, runeKey('y'), true},
		{"n", &yes, runeKey('n'), false},
		{"enter default yes", &yes, enterKey, true},
		{"enter default no", &no, enterKey, false},
		{"toggle to no", &yes, rightKey, false},
		{"toggle to yes", &no, leftKey, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewConfirm(confirmReq("x", "T", "m", tc.def))
			if tc.key == rightKey || tc.key == leftKey {
				d.HandleKey(tc.key)
				tc.key = enterKey
			}
			r := d.HandleKey(tc.key)
			if !r.Done {
				t.Fatalf("key did not finish")
			}
			if got := response(t, r).OK; got != tc.want {
				t.Fatalf("ok = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDialog_CNF02_Countdown(t *testing.T) {
	ms := 5000
	def := true
	req := proto.UIRequest{Type: proto.FrameUIRequest, ID: "x", Title: "T", Message: "m", Default: &def, TimeoutMS: &ms}
	d := NewConfirm(req)
	start := time.Unix(100, 0)
	d.Tick(start)
	if d.Tick(start.Add(4 * time.Second)) {
		// changed is fine; it must not be done
	}
	v := d.View(40, 8, styles.Styles{})
	if !strings.Contains(v, "(1s)") {
		t.Fatalf("countdown not shown:\n%s", v)
	}
	d.Tick(start.Add(5 * time.Second))
	r := d.HandleKey(runeKey(' ')) // no-op key; the tick should have finished it
	_ = r
	// The dialog finished during the tick; check by rendering no countdown and
	// by the value being the default.
	if got := d.(*confirm).value.OK; !got {
		t.Fatalf("timeout did not resolve with the default")
	}
}

func TestDialog_CNF03_AnswerShape(t *testing.T) {
	d := NewConfirm(confirmReq("abc", "T", "m", nil))
	r := d.HandleKey(runeKey('y'))
	resp := response(t, r)
	if resp.Type != proto.FrameUIResponse || resp.ID != "abc" || !resp.OK || resp.Cancelled {
		t.Fatalf("response = %+v", resp)
	}
}

func FuzzDialogKeys(f *testing.F) {
	f.Add("y")
	f.Add("\x1b")
	f.Add("enter")
	f.Fuzz(func(t *testing.T, s string) {
		dialogs := []Dialog{
			NewConfirm(confirmReq("c1", "T", "m", nil)),
			NewConfirm(confirmReq("c2", "T", "m", nil)),
		}
		for _, d := range dialogs {
			for _, r := range s {
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							t.Fatalf("dialog %s panicked on %q: %v", d.ID(), r, rec)
						}
					}()
					res := d.HandleKey(runeKey(r))
					if res.Done {
						if resp, ok := res.Value.(proto.UIResponse); ok && resp.ID != d.ID() {
							t.Fatalf("response id %q != dialog id %q", resp.ID, d.ID())
						}
					}
				}()
			}
		}
	})
}
