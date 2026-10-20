//go:build !notui

package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/client"
)

func bashResponse(content string, isError bool) client.ResponseMsg {
	data, _ := json.Marshal(map[string]any{"content": content, "is_error": isError})
	return client.ResponseMsg{Response: proto.Response{Command: proto.CmdBash, OK: true, Data: data}}
}

func TestUI_EDI25_BashSendsTheCommandWithoutTheBang(t *testing.T) {
	m, f, _ := newModel(t)
	m.setEditorText("!echo hi")
	do(update(m, enterKey))
	if len(f.sends) != 1 || f.sends[0].Type != proto.CmdBash || f.sends[0].Command != "echo hi" {
		t.Fatalf("sends = %+v, want bash with the command and no leading !", f.sends)
	}
}

func TestUI_EDI26_BashOutputIsShown(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, bashResponse("first line\nsecond line\n", false))
	if m.overlay.Len() != 1 {
		t.Fatalf("overlay depth = %d, want the output dialog", m.overlay.Len())
	}
	if v := m.render(); !strings.Contains(v, "first line") || !strings.Contains(v, "second line") {
		t.Fatalf("output missing from the view:\n%s", v)
	}
}

func TestUI_EDI27_BashOutputIsUntrusted(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, bashResponse("visible\x1b]0;pwned\x07 text\x1b[2J\rtail\tend", false))
	v := m.render()
	if !strings.Contains(v, "visible") {
		t.Fatalf("output missing from the view:\n%s", v)
	}
	for _, bad := range []string{"pwned", "\x1b[2J", "\r", "\t"} {
		if strings.Contains(v, bad) {
			t.Fatalf("shell output reached the screen with %q:\n%q", bad, v)
		}
	}
}

func TestUI_EDI28_BashOutputIsBounded(t *testing.T) {
	var b strings.Builder
	for i := range 5 * bashOutputLines {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	lines := outputLines(b.String(), bashOutputLines)
	if len(lines) != bashOutputLines+1 {
		t.Fatalf("got %d lines, want the cap plus a truncation note", len(lines))
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, fmt.Sprint(4*bashOutputLines)) {
		t.Fatalf("truncation note = %q, want the count of dropped lines", last)
	}
	if lines[0] != "line 0" || lines[bashOutputLines-1] != fmt.Sprintf("line %d", bashOutputLines-1) {
		t.Fatalf("the kept lines are not the first %d", bashOutputLines)
	}
}

func TestUI_EDI29_BashWithNoOutputSaysSo(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, bashResponse("", false))
	if v := m.render(); !strings.Contains(v, "no output") {
		t.Fatalf("an empty result should say so:\n%s", v)
	}
}

func TestUI_EDI30_BashErrorBecomesANotice(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, client.ResponseMsg{Response: proto.Response{
		Command: proto.CmdBash, OK: false, Error: &proto.Error{Message: "bash: command is required"},
	}})
	if m.notice != "bash: command is required" || m.overlay.Len() != 0 {
		t.Fatalf("notice = %q, overlay = %d", m.notice, m.overlay.Len())
	}
}
