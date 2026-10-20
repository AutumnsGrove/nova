//go:build !notui

package dialog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// permReq builds a permission request for tests.
func permReq(remember bool, def *bool) proto.UIRequest {
	return proto.UIRequest{
		Type: proto.FrameUIRequest, ID: "p1", Method: "ui.confirm",
		Title: "Allow read?", Tool: "read", Message: "rule: ask",
		Args: json.RawMessage(`{"path":"a.go"}`), Remember: remember, Default: def,
	}
}

func TestPermission_PRD01_ShowsToolSummaryReason(t *testing.T) {
	d := NewPermission(permReq(true, nil), false)
	v := d.View(60, 20, styles.Styles{})
	for _, want := range []string{"read", "rule: ask"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}
	if sum := render.Summarize("read", json.RawMessage(`{"path":"a.go"}`), 40); sum != "" && !strings.Contains(v, sum) {
		t.Fatalf("view missing summary %q:\n%s", sum, v)
	}
}

func TestPermission_PRD02_ThreeChoices(t *testing.T) {
	d := NewPermission(permReq(true, nil), false)
	v := d.View(60, 20, styles.Styles{})
	for _, want := range []string{"Allow once", "Allow for this session", "Deny"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}
}

func TestPermission_PRD03_DefaultDeny(t *testing.T) {
	d := NewPermission(permReq(true, nil), false)
	p := d.(*permission)
	if p.choice != 2 {
		t.Fatalf("default choice = %d, want deny", p.choice)
	}
	allow := true
	d2 := NewPermission(permReq(true, &allow), false)
	if d2.(*permission).choice != 0 {
		t.Fatalf("allow default was not preselected")
	}
}

func TestPermission_PRD04_SessionAnswer(t *testing.T) {
	d := NewPermission(permReq(true, nil), false)
	r := d.HandleKey(runeKey('s'))
	resp := response(t, r)
	if !resp.OK || resp.Remember != "session" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestPermission_PRD05_AllowOnce(t *testing.T) {
	d := NewPermission(permReq(true, nil), false)
	resp := response(t, d.HandleKey(runeKey('y')))
	if !resp.OK || resp.Remember != "" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestPermission_PRD06_Deny(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{runeKey('n'), escKey} {
		d := NewPermission(permReq(true, nil), false)
		resp := response(t, d.HandleKey(key))
		if resp.OK {
			t.Fatalf("key %v did not deny: %+v", key, resp)
		}
	}
}

func TestPermission_PRD07_TabExpands(t *testing.T) {
	args := json.RawMessage(`{"a":1}`)
	d := NewPermission(proto.UIRequest{Type: proto.FrameUIRequest, ID: "p", Tool: "read", Args: args, Remember: true}, false)
	if strings.Contains(d.View(60, 30, styles.Styles{}), "arguments:") {
		t.Fatal("arguments shown before expanding")
	}
	d.HandleKey(tabKey)
	v := d.View(60, 40, styles.Styles{})
	if !strings.Contains(v, "arguments:") || !strings.Contains(v, "\"a\"") {
		t.Fatalf("expanded view:\n%s", v)
	}

	big := json.RawMessage("{" + func() string {
		var parts []string
		for i := 0; i < 40; i++ {
			parts = append(parts, `"k`+string(rune('a'+i))+`":`+`"v"`)
		}
		return strings.Join(parts, ",")
	}() + "}")
	d2 := NewPermission(proto.UIRequest{Type: proto.FrameUIRequest, ID: "p", Tool: "read", Args: big, Remember: true}, true)
	if v2 := d2.View(60, 40, styles.Styles{}); !strings.Contains(v2, "more") {
		t.Fatalf("long args were not capped:\n%s", v2)
	}
}

func TestPermission_PRD08_HostileArgs(t *testing.T) {
	raw := json.RawMessage(`{"path":"` + strings.Repeat("A", 1<<20) + `\u001b[31mred"}`)
	d := NewPermission(proto.UIRequest{Type: proto.FrameUIRequest, ID: "p", Tool: "read", Args: raw, Message: "\x1b[31mred\x1b[0m reason", Remember: true}, true)
	v := d.View(50, 20, styles.Styles{})
	for _, l := range strings.Split(v, "\n") {
		if ansi.StringWidth(l) > 50 {
			t.Fatalf("line too wide: %d %q", ansi.StringWidth(l), l)
		}
		if strings.Contains(l, "\x1b[31m") {
			t.Fatalf("control sequence survived: %q", l)
		}
	}
}

func TestPermission_PRD09_NoRemember(t *testing.T) {
	d := NewPermission(permReq(false, nil), false)
	v := d.View(60, 20, styles.Styles{})
	if strings.Contains(v, "session") || strings.Contains(v, "(s)") {
		t.Fatalf("session choice shown without remember:\n%s", v)
	}
	r := d.HandleKey(runeKey('s'))
	if r.Done {
		t.Fatal("s should do nothing when remember is not offered")
	}
}

// The prompt shows what is about to run. A carriage return, a backspace or a
// bidirectional override in it would be drawn as something else, so no field of
// the request may reach the screen with one.
func TestPermission_PRD20_NothingTheTerminalActsOnIsDrawn(t *testing.T) {
	hostile := "ok\rrm -rf /\b\b\a\x00 \u202eexe.txt\u2066"
	req := permReq(true, nil)
	req.Title = "Allow " + hostile + "?"
	req.Tool = "bash"
	req.Message = "rule " + hostile
	req.Args = json.RawMessage(`{"command":` + strconvQuote(hostile) + `}`)
	for _, expanded := range []bool{false, true} {
		v := NewPermission(req, expanded).View(80, 30, styles.Styles{})
		for _, bad := range []string{"\r", "\b", "\a", "\x00", "\x7f", "\u202e", "\u2066", "\u2069"} {
			if strings.Contains(v, bad) {
				t.Errorf("expanded=%v: the view contains %q:\n%q", expanded, bad, v)
			}
		}
		if !strings.Contains(v, "rm -rf /") {
			t.Errorf("expanded=%v: the command text itself is missing:\n%s", expanded, v)
		}
	}
}

// strconvQuote is JSON string quoting for a test literal.
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
