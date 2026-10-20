//go:build !notui

package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/diag"
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
	for i := range 5 * overlayMaxLines {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	lines := outputLines(b.String(), overlayMaxLines)
	if len(lines) != overlayMaxLines+1 {
		t.Fatalf("got %d lines, want the cap plus a truncation note", len(lines))
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, fmt.Sprint(4*overlayMaxLines)) {
		t.Fatalf("truncation note = %q, want the count of dropped lines", last)
	}
	if lines[0] != "line 0" || lines[overlayMaxLines-1] != fmt.Sprintf("line %d", overlayMaxLines-1) {
		t.Fatalf("the kept lines are not the first %d", overlayMaxLines)
	}
}

func TestUI_EDI29_BashWithNoOutputSaysSo(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, bashResponse("", false))
	if v := m.render(); !strings.Contains(v, "no output") {
		t.Fatalf("an empty result should say so:\n%s", v)
	}
}

func TestUI_LOG03_ReadTailKeepsTheEndOfALargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		n    int64
		want string
	}{{10, "6789abcdef"}, {16, "0123456789abcdef"}, {100, "0123456789abcdef"}} {
		got, err := readTail(path, c.n)
		if err != nil || string(got) != c.want {
			t.Fatalf("readTail(%d) = %q, %v; want %q", c.n, got, err, c.want)
		}
	}
	if _, err := readTail(filepath.Join(t.TempDir(), "missing"), 10); err == nil {
		t.Fatal("a missing file should be an error")
	}
}

func TestUI_LOG04_CrashLogIsUntrusted(t *testing.T) {
	rec := diag.New(diag.Options{Dir: t.TempDir()})
	diag.SetDefault(rec)
	defer diag.SetDefault(nil)
	if _, err := rec.Report("panic text\x1b]0;pwned\x07 kept", "tui view"); err != nil {
		t.Fatal(err)
	}
	m, _, _ := newModel(t)
	cmd, _ := m.localCommand("crash", "")
	update(m, do(cmd))
	v := m.View().Content
	if !strings.Contains(v, "panic text") || strings.Contains(v, "pwned") {
		t.Fatalf("crash log must show the report without its escape sequences:\n%q", v)
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
