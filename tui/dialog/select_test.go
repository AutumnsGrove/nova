//go:build !notui

package dialog

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// opts builds n options.
func opts(n int) []proto.UIOption {
	out := make([]proto.UIOption, n)
	for i := range out {
		out[i] = proto.UIOption{ID: string(rune('a' + i)), Label: "Option", Description: "desc"}
	}
	return out
}

func TestSelect_SEL01_Single(t *testing.T) {
	d := NewSelect(proto.UIRequest{Type: proto.FrameUIRequest, ID: "s", Options: []proto.UIOption{
		{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"},
	}})
	r := d.HandleKey(enterKey)
	if !r.Done {
		t.Fatal("enter did not finish")
	}
	if got, ok := r.Value.([]string); !ok || len(got) != 1 || got[0] != "a" {
		t.Fatalf("value = %#v", r.Value)
	}
}

func TestSelect_SEL02_MultiOrder(t *testing.T) {
	d := NewSelect(proto.UIRequest{Type: proto.FrameUIRequest, ID: "s", Multi: true,
		Options: []proto.UIOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}, {ID: "c", Label: "C"}}})
	d.HandleKey(downKey)
	d.HandleKey(spaceKey)
	d.HandleKey(upKey)
	d.HandleKey(spaceKey)
	r := d.HandleKey(enterKey)
	got := r.Value.([]string)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("value = %v, want [a b] in option order", got)
	}
}

func TestSelect_SEL03_Filter(t *testing.T) {
	d := NewSelect(proto.UIRequest{Type: proto.FrameUIRequest, ID: "s",
		Options: []proto.UIOption{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}, {ID: "c", Label: "Delta"}}})
	for _, r := range "TA" {
		d.HandleKey(runeKey(r))
	}
	vis := d.(*selectDialog).visible()
	if len(vis) != 2 || vis[0].ID != "b" || vis[1].ID != "c" {
		t.Fatalf("visible = %v", vis)
	}
}

func TestSelect_SEL04_NoMatches(t *testing.T) {
	d := NewSelect(proto.UIRequest{Type: proto.FrameUIRequest, ID: "s",
		Options: []proto.UIOption{{ID: "a", Label: "Alpha"}}})
	d.HandleKey(runeKey('z'))
	if v := d.View(50, 10, styles.Styles{}); !strings.Contains(v, "no matches") {
		t.Fatalf("view:\n%s", v)
	}
	if r := d.HandleKey(enterKey); r.Done {
		t.Fatal("enter on an empty filter result must do nothing")
	}
}

func TestSelect_SEL05_Descriptions(t *testing.T) {
	st := styles.Build(styles.Dark(), colorprofile.TrueColor)
	d := NewSelect(proto.UIRequest{Type: proto.FrameUIRequest, ID: "s",
		Options: []proto.UIOption{{ID: "a", Label: "Alpha", Description: "the first"}}})
	v := d.View(60, 10, st)
	if !strings.Contains(v, "the first") {
		t.Fatalf("description missing:\n%s", v)
	}
}

func TestSelect_SEL06_ManyOptions(t *testing.T) {
	d := NewSelect(proto.UIRequest{Type: proto.FrameUIRequest, ID: "s", Options: opts(500)})
	w, h := d.Size(80, 24)
	if h > 24 {
		t.Fatalf("height %d exceeds max", h)
	}
	v := d.View(w, h, styles.Styles{})
	for _, l := range strings.Split(v, "\n") {
		if ansi.StringWidth(l) > w {
			t.Fatalf("line too wide: %d", ansi.StringWidth(l))
		}
	}
	d.HandleKey(pgdownKey)
	if d.(*selectDialog).cursor == 0 {
		t.Fatal("pgdown did not move the cursor")
	}
}

func TestSelect_SEL07_Esc(t *testing.T) {
	d := NewSelect(proto.UIRequest{Type: proto.FrameUIRequest, ID: "s", Options: opts(3)})
	r := d.HandleKey(escKey)
	if !r.Done || !r.Cancelled {
		t.Fatalf("result = %+v", r)
	}
}

func TestInput_INP01_Submit(t *testing.T) {
	d := NewInput(proto.UIRequest{Type: proto.FrameUIRequest, ID: "i"})
	d.HandleKey(runeKey('h'))
	d.HandleKey(runeKey('i'))
	r := d.HandleKey(enterKey)
	if !r.Done || r.Value != "hi" {
		t.Fatalf("result = %+v", r)
	}
}

func TestInput_INP02_PlaceholderAndDefault(t *testing.T) {
	d := NewInput(proto.UIRequest{Type: proto.FrameUIRequest, ID: "i", Placeholder: "type here"})
	if v := d.View(40, 6, styles.Styles{}); !strings.Contains(v, "type here") {
		t.Fatalf("placeholder missing:\n%s", v)
	}
	d2 := NewInput(proto.UIRequest{Type: proto.FrameUIRequest, ID: "i", Text: "default"})
	if r := d2.HandleKey(enterKey); r.Value != "default" {
		t.Fatalf("default not returned: %#v", r.Value)
	}
}

func TestInput_INP03_Paste(t *testing.T) {
	d := NewInput(proto.UIRequest{Type: proto.FrameUIRequest, ID: "i"})
	d.(Paster).Paste("a\nb\r\nc")
	if r := d.HandleKey(enterKey); r.Value != "a b c" {
		t.Fatalf("paste value = %#v", r.Value)
	}
}

func TestInput_INP04_Esc(t *testing.T) {
	d := NewInput(proto.UIRequest{Type: proto.FrameUIRequest, ID: "i"})
	d.HandleKey(runeKey('x'))
	r := d.HandleKey(escKey)
	if !r.Done || !r.Cancelled || r.Value != "" {
		t.Fatalf("result = %+v", r)
	}
}

func TestInput_INP05_WideChars(t *testing.T) {
	d := NewInput(proto.UIRequest{Type: proto.FrameUIRequest, ID: "i"})
	for _, r := range "日本語" {
		d.HandleKey(runeKey(r))
	}
	for _, l := range strings.Split(d.View(20, 6, styles.Styles{}), "\n") {
		if ansi.StringWidth(l) > 20 {
			t.Fatalf("line too wide: %d", ansi.StringWidth(l))
		}
	}
	d.HandleKey(leftKey)
	if got := d.(*inputDialog).cursor; got != 2 {
		t.Fatalf("cursor = %d, want 2 after one left", got)
	}
}
