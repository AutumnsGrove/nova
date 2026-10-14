//go:build !notui

package ui

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/proto"
)

// usageResponse builds a get_usage response with the given JSON data.
func usageResponse(data string) proto.Response {
	return proto.Response{Type: proto.FrameResponse, Command: proto.CmdGetUsage, OK: true, Data: []byte(data)}
}

func TestUsage_USG23_Overlay(t *testing.T) {
	m, f, _ := newModel(t)
	f.responses[proto.CmdGetUsage] = usageResponse(`{"rows":["Rolling 5h █████ 62.5% $12.00 / $19.20 1h 12m","Weekly ███ 31% $30.10 / $96.00","Monthly ████ 44% $42.30 / $96.00"],"footer":"go 5h 62.5% (1h 12m) · wk 31% · mo 44%"}`)
	cmd, handled := m.localCommand("usage", "")
	if !handled {
		t.Fatal("/usage was not handled locally")
	}
	update(m, do(cmd))
	if m.overlay.Len() != 1 {
		t.Fatalf("overlay = %d, want 1", m.overlay.Len())
	}
	view := m.overlay.Top().View(70, 12, m.st)
	if !strings.Contains(view, "Rolling 5h") || !strings.Contains(view, "Monthly") {
		t.Fatalf("overlay missing rows:\n%s", view)
	}
	update(m, escKey)
	if m.overlay.Len() != 0 {
		t.Fatal("esc did not close the usage overlay")
	}
}

func TestUsage_USG24_FooterWidget(t *testing.T) {
	m, f, _ := newModel(t)
	f.responses[proto.CmdGetUsage] = usageResponse(`{"rows":["x"],"footer":"go 5h 62.5% (1h 12m) · wk 31% · mo 44%"}`)
	cmd, _ := m.localCommand("usage", "")
	update(m, do(cmd))
	if !strings.Contains(m.footer(), "go 5h 62.5%") {
		t.Fatalf("footer = %q", m.footer())
	}
}

func TestUsage_USG25_Cadence(t *testing.T) {
	m, f, _ := newModel(t)
	cmd := m.scheduleUsage()
	if cmd == nil {
		t.Fatal("no usage timer armed")
	}
	if _, ok := do(cmd).(usageTickMsg); !ok {
		t.Fatalf("timer yielded %T, want usageTickMsg", do(cmd))
	}
	before := len(f.sends)
	execAll(update(m, usageTickMsg{}))
	if len(f.sends) != before+1 || f.sends[len(f.sends)-1].Type != proto.CmdGetUsage {
		t.Fatalf("sends = %+v, want one get_usage", f.sends)
	}
}

func TestUsage_USG26_NoKeyNoStatus(t *testing.T) {
	m, f, _ := newModel(t)
	f.responses[proto.CmdGetUsage] = usageResponse(`{"error":"Not signed in. Run ` + "`nova login opencode-go`" + `."}`)
	cmd, _ := m.localCommand("usage", "")
	update(m, do(cmd))
	if strings.Contains(m.footer(), "go 5h") {
		t.Fatalf("footer shows usage without a key: %q", m.footer())
	}
	if m.overlay.Len() != 1 {
		t.Fatalf("overlay = %d, want the sign-in message", m.overlay.Len())
	}
	if view := m.overlay.Top().View(70, 12, m.st); !strings.Contains(view, "Not signed in") {
		t.Fatalf("overlay = %q", view)
	}
}
