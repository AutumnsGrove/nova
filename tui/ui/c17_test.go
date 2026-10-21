//go:build !notui

package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/client"
	"github.com/AutumnsGrove/nova/tui/dialog"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// uiReq builds a UI request for tests.
func uiReq(id, method string) proto.UIRequest {
	return proto.UIRequest{Type: proto.FrameUIRequest, ID: id, Method: method, Title: "T"}
}

// pushUI delivers a UI request to the model and runs any command.
func pushUI(m *Model, req proto.UIRequest) { execAll(update(m, client.UIRequestMsg{UIRequest: req})) }

// key sends one key and runs the resulting commands.
func key(m *Model, k tea.KeyPressMsg) { execAll(update(m, k)) }

// lastSend returns the last command the fake client recorded.
func lastSend(f *fakeClient) proto.Command {
	if len(f.sends) == 0 {
		return proto.Command{}
	}
	return f.sends[len(f.sends)-1]
}

// sent reports whether any recorded command has the given type.
func sent(f *fakeClient, typ string) bool {
	for _, c := range f.sends {
		if c.Type == typ {
			return true
		}
	}
	return false
}

func TestUI_KYA01_KeysAreLive(t *testing.T) {
	m, f, _ := newModel(t)
	key(m, ctrlKey('l'))
	if !sent(f, proto.CmdListModels) {
		t.Fatalf("ctrl+l did not request models: %+v", f.sends)
	}
	key(m, ctrlKey('r'))
	if !sent(f, proto.CmdSessionList) {
		t.Fatalf("ctrl+r did not request sessions: %+v", f.sends)
	}
	key(m, ctrlKey('t'))
	if !sent(f, proto.CmdSetThinking) {
		t.Fatalf("ctrl+t did not set thinking: %+v", f.sends)
	}
}

func TestUI_KYA02_ThinkingCycles(t *testing.T) {
	m, _, _ := newModel(t)
	want := []string{"low", "medium", "high", "off"}
	for i, w := range want {
		key(m, ctrlKey('t'))
		if m.state.Thinking != w {
			t.Fatalf("press %d: thinking = %q, want %q", i+1, m.state.Thinking, w)
		}
	}
}

func TestUI_KYA03_QuestionMarkHelp(t *testing.T) {
	m, _, _ := newModel(t)
	key(m, runeKey('?'))
	if m.overlay.Len() != 1 {
		t.Fatal("? with an empty editor did not open help")
	}

	m2, _, _ := newModel(t)
	key(m2, runeKey('x'))
	key(m2, runeKey('?'))
	if m2.overlay.Len() != 0 {
		t.Fatal("? with text opened help")
	}
	if !strings.Contains(m2.editor.Value(), "?") {
		t.Fatalf("editor = %q, want a typed ?", m2.editor.Value())
	}
}

func TestUI_UXB01_MethodMapping(t *testing.T) {
	cases := []struct {
		method string
		want   string
		req    proto.UIRequest
	}{
		{"confirm", "Yes", uiReq("c", "confirm")},
		{"select", "Alpha", proto.UIRequest{Type: proto.FrameUIRequest, ID: "s", Method: "select",
			Options: []proto.UIOption{{ID: "a", Label: "Alpha"}}}},
		{"input", "type here", proto.UIRequest{Type: proto.FrameUIRequest, ID: "i", Method: "input", Placeholder: "type here"}},
		{"form", "Pick?", proto.UIRequest{Type: proto.FrameUIRequest, ID: "f", Method: "form",
			Questions: []proto.UIQuestion{{ID: "q", Prompt: "Pick?", Kind: "text"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			m, _, _ := newModel(t)
			pushUI(m, tc.req)
			if m.overlay.Len() != 1 {
				t.Fatalf("no dialog for %s", tc.method)
			}
			if v := m.overlay.Top().View(70, 14, m.st); !strings.Contains(v, tc.want) {
				t.Fatalf("dialog for %s missing %q:\n%s", tc.method, tc.want, v)
			}
		})
	}
}

func TestUI_UXB02_UnknownMethod(t *testing.T) {
	m, f, _ := newModel(t)
	m.caps["ui.bogus"] = true
	pushUI(m, uiReq("b1", "bogus"))
	if !strings.Contains(m.notice, "unsupported") {
		t.Fatalf("notice = %q", m.notice)
	}
	if len(f.answers) != 1 || f.answers[0].ID != "b1" || !f.answers[0].Cancelled {
		t.Fatalf("answers = %+v", f.answers)
	}
}

func TestUI_UXB03_RequestIDPreserved(t *testing.T) {
	m, f, _ := newModel(t)
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "keep-me", Method: "select",
		Options: []proto.UIOption{{ID: "a", Label: "A"}}})
	key(m, runeKey('x'))
	key(m, escKey)
	if len(f.answers) != 1 || f.answers[0].ID != "keep-me" {
		t.Fatalf("answers = %+v", f.answers)
	}
}

func TestUI_UXB04_OneAnswerPerID(t *testing.T) {
	m, f, _ := newModel(t)
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "once", Method: "select",
		Options: []proto.UIOption{{ID: "a", Label: "A"}}})
	key(m, escKey)
	execAll(m.answerUI("once", cancelledResult()))
	if len(f.answers) != 1 {
		t.Fatalf("got %d answers, want 1", len(f.answers))
	}
}

func TestUI_UXB05_FireAndForget(t *testing.T) {
	m, f, _ := newModel(t)
	pushUI(m, proto.UIRequest{Method: "notify", Text: "hello", Level: "warn"})
	pushUI(m, proto.UIRequest{Method: "status", Key: "k", Segments: []proto.UISegment{{Text: "s", Style: "accent"}}})
	pushUI(m, proto.UIRequest{Method: "widget", Key: "w", Placement: "above_editor",
		Content: []byte(`{"kind":"text","lines":["body"]}`)})
	pushUI(m, proto.UIRequest{Method: "title", Text: "my title"})
	pushUI(m, proto.UIRequest{Method: "set_editor_text", Text: "set"})
	if len(f.answers) != 0 {
		t.Fatalf("fire-and-forget produced answers: %+v", f.answers)
	}
	if m.overlay.Len() != 0 {
		t.Fatal("fire-and-forget opened a dialog")
	}
	if m.title != "my title" {
		t.Fatalf("title = %q", m.title)
	}
	if m.widgets.Height("above_editor", 40, 8) != 1 {
		t.Fatal("widget was not set")
	}
	if len(m.widgets.StatusSegments()) == 0 {
		t.Fatal("status segment was not set")
	}
	if m.editor.Value() != "set" {
		t.Fatalf("editor = %q, want set", m.editor.Value())
	}
	if len(m.widgets.Toasts(m.clock.Now(), 40, m.st)) == 0 {
		t.Fatal("toast was not recorded")
	}
}

func TestUI_UXB06_EditorTextWhileTyping(t *testing.T) {
	m, _, clk := newModel(t)
	m.setEditorText("typed")
	m.lastInput = clk.now
	pushUI(m, proto.UIRequest{Method: "set_editor_text", Text: "replaced"})
	if m.editor.Value() != "typed" {
		t.Fatalf("editor was replaced while typing: %q", m.editor.Value())
	}
	if m.notice == "" {
		t.Fatal("no notice for a deferred set_editor_text")
	}
	clk.now = clk.now.Add(3 * time.Second)
	pushUI(m, proto.UIRequest{Method: "set_editor_text", Text: "replaced"})
	if m.editor.Value() != "replaced" {
		t.Fatalf("editor = %q, want replaced", m.editor.Value())
	}
}

func TestUI_UXB07_UndeclaredIgnored(t *testing.T) {
	m, f, _ := newModel(t)
	delete(m.caps, "ui.select")
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "x", Method: "select",
		Options: []proto.UIOption{{ID: "a", Label: "A"}}})
	if m.overlay.Len() != 0 {
		t.Fatal("an undeclared method opened a dialog")
	}
	if len(f.answers) != 0 {
		t.Fatal("an undeclared method was answered")
	}
}

func TestUI_UXB08_HelloCapabilities(t *testing.T) {
	want := []string{"ui.confirm", "ui.select", "ui.input", "ui.form",
		"ui.notify", "ui.status", "ui.widget", "ui.title", "ui.set_editor_text"}
	got := capabilitySet(capabilities)
	for _, w := range want {
		if !got[w] {
			t.Fatalf("capability %s is not declared", w)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("capabilities = %v, want %v", got, want)
	}
}

func TestUI_DLG02_QueuedRequests(t *testing.T) {
	m, f, _ := newModel(t)
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "first", Method: "select",
		Options: []proto.UIOption{{ID: "a", Label: "A"}}})
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "second", Method: "select",
		Options: []proto.UIOption{{ID: "b", Label: "B"}}})
	if m.overlay.Top().ID() != "first" {
		t.Fatalf("top = %s, want first", m.overlay.Top().ID())
	}
	key(m, escKey)
	if m.overlay.Top() == nil || m.overlay.Top().ID() != "second" {
		t.Fatalf("second request was not shown after the first closed")
	}
	key(m, escKey)
	if len(f.answers) != 2 {
		t.Fatalf("answers = %+v, want 2", f.answers)
	}
}

func TestUI_DLG04_DialogWhileStreaming(t *testing.T) {
	m, _, _ := newModel(t)
	m.transcript.AddUser("seed", nil)
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "d", Method: "select",
		Options: []proto.UIOption{{ID: "a", Label: "A"}}})
	before := len(m.transcript.Items())
	execAll(update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}}))
	m.transcript.AddUser("during", nil)
	m.syncList()
	if m.overlay.Len() != 1 {
		t.Fatal("the dialog closed while the transcript streamed")
	}
	if len(m.transcript.Items()) != before+1 {
		t.Fatalf("transcript items = %d, want %d", len(m.transcript.Items()), before+1)
	}
}

func TestUI_DLG08_ExpiredCloses(t *testing.T) {
	m, _, _ := newModel(t)
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "gone", Method: "select",
		Options: []proto.UIOption{{ID: "a", Label: "A"}}})
	execAll(update(m, client.EventMsg{Event: proto.Event{Type: proto.EventUIExpired, ID: "gone"}}))
	if m.overlay.Len() != 0 {
		t.Fatal("ui_expired did not close the dialog")
	}
	if !strings.Contains(m.notice, "timed out") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestUI_DLG09_QuitCancelsAll(t *testing.T) {
	m, f, _ := newModel(t)
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "a", Method: "select",
		Options: []proto.UIOption{{ID: "x", Label: "X"}}})
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "b", Method: "select",
		Options: []proto.UIOption{{ID: "y", Label: "Y"}}})
	key(m, ctrlKey('c')) // arm quit
	execAll(update(m, ctrlKey('c')))
	if len(f.answers) != 2 {
		t.Fatalf("answers = %+v, want both cancelled", f.answers)
	}
	for _, a := range f.answers {
		if !a.Cancelled {
			t.Fatalf("answer %s was not cancelled", a.ID)
		}
	}
}

func TestUI_SSL04_NotWhileRunning(t *testing.T) {
	m, f, _ := newModel(t)
	m.runActive = true
	key(m, ctrlKey('r'))
	if sent(f, proto.CmdSessionList) {
		t.Fatal("requested the session list while a run is active")
	}
	if !strings.Contains(m.notice, "active") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestUI_WDG09_SidebarUnderEditor(t *testing.T) {
	m, _, _ := newModel(t)
	pushUI(m, proto.UIRequest{Method: "widget", Key: "side", Placement: "sidebar",
		Content: []byte(`{"kind":"text","lines":["SIDEBAR"]}`)})
	m.recompute()
	if m.layout.Sidebar.W != 0 {
		t.Fatal("a narrow terminal should not have a sidebar")
	}
	found := -1
	for i, l := range strings.Split(m.render(), "\n") {
		if strings.Contains(l, "SIDEBAR") {
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("the sidebar widget was not drawn:\n%s", m.render())
	}
	if found < m.layout.Editor.Y+m.layout.Editor.H {
		t.Fatalf("the sidebar widget was drawn above the editor (row %d)", found)
	}
}

func TestUI_STS04_NarrowWrapsExtensionStatus(t *testing.T) {
	m, _, _ := newModel(t)
	pushUI(m, proto.UIRequest{Method: "status", Key: "ext",
		Segments: []proto.UISegment{{Text: "EXTENSION-STATUS"}}})
	m.width = 120
	m.recompute()
	if wide := footerText(m); !strings.Contains(wide, "EXTENSION-STATUS") || !strings.Contains(wide, "$0.000") {
		t.Fatalf("wide footer = %q", wide)
	}
	m.width = 30
	m.recompute()
	narrow := footerText(m)
	if !strings.Contains(narrow, "EXTENSION-STATUS") {
		t.Fatalf("narrow status dropped the extension instead of wrapping it: %q", narrow)
	}
	if !strings.Contains(narrow, "$0.000") {
		t.Fatalf("narrow status dropped a built-in segment: %q", narrow)
	}
}

func TestUI_TST04_ToastsDoNotTakeFocus(t *testing.T) {
	m, _, _ := newModel(t)
	pushUI(m, proto.UIRequest{Method: "notify", Text: "hi", Level: "info"})
	key(m, runeKey('a'))
	if m.editor.Value() != "a" {
		t.Fatalf("editor = %q; a toast stole the key", m.editor.Value())
	}
}

// cancelledResult is a cancelled dialog result for tests.
func cancelledResult() dialog.Result { return dialog.Result{Done: true, Cancelled: true} }

// nova --resume starts with the session picker already asked for, the way the
// /resume command asks for it.
func TestUI_RSM01_ResumeOptionAsksForTheSessionList(t *testing.T) {
	f := &fakeClient{responses: map[string]proto.Response{}}
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	m := New(Options{Client: f, Theme: styles.Mono(), Clock: clk, CWD: t.TempDir(), Profile: colorprofile.ASCII, Renderer: echoRenderer{}, Width: 80, Height: 24, OpenSessions: true})
	execAll(m.Init())
	if !sent(f, proto.CmdSessionList) {
		t.Fatalf("Init did not ask for the session list: %+v", f.sends)
	}
	plain, pf, _ := newModel(t)
	execAll(plain.Init())
	if sent(pf, proto.CmdSessionList) {
		t.Fatal("an ordinary start asked for the session list")
	}
}
