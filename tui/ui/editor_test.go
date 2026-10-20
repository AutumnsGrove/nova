//go:build !notui

package ui

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/client"
)

// prepared runs ctrl+g's first command and returns the editor start message.
func prepared(t *testing.T, m *Model) editorStartMsg {
	t.Helper()
	msg, ok := do(m.openEditor()).(editorStartMsg)
	if !ok {
		t.Fatal("openEditor did not yield an editorStartMsg")
	}
	if msg.path != "" {
		t.Cleanup(func() { _ = os.Remove(msg.path) })
	}
	return msg
}

func runEndWith(m *Model, dropped *proto.DroppedQueue) {
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunEnd, Reason: "aborted", DroppedQueue: dropped}})
}

func TestUI_RUN06_DroppedQueueReturnsToTheEditor(t *testing.T) {
	m, _, _ := newModel(t)
	runEndWith(m, &proto.DroppedQueue{Steering: []string{"s1", "s2"}, FollowUp: []string{"f1"}})
	if got := m.editor.Value(); got != "s1\ns2\nf1" {
		t.Fatalf("editor = %q, want steering then follow-up", got)
	}
}

func TestUI_RUN07_DroppedQueueKeepsTheDraft(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("my draft")
	runEndWith(m, &proto.DroppedQueue{FollowUp: []string{"f1"}})
	if got := m.editor.Value(); got != "my draft\nf1" {
		t.Fatalf("editor = %q, want the draft kept and the dropped text after it", got)
	}
}

func TestUI_RUN08_NoDroppedQueueLeavesTheEditor(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("draft")
	runEndWith(m, nil)
	runEndWith(m, &proto.DroppedQueue{})
	if got := m.editor.Value(); got != "draft" {
		t.Fatalf("editor = %q, want it untouched", got)
	}
}

func TestUI_EDI31_EqualLengthPastesStaySeparate(t *testing.T) {
	m, f, _ := newModel(t)
	a, b := strings.Repeat("a", 9000), strings.Repeat("b", 9000)
	update(m, tea.PasteMsg{Content: a})
	update(m, tea.PasteMsg{Content: b})
	do(update(m, enterKey))
	if len(f.sends) != 1 || f.sends[0].Text != a+b {
		t.Fatalf("two pastes of equal length were merged: sent %d bytes, a=%d b=%d",
			len(f.sends[0].Text), strings.Count(f.sends[0].Text, "a"), strings.Count(f.sends[0].Text, "b"))
	}
}

func TestUI_EDI32_PasteLimitCountsCharacters(t *testing.T) {
	m, _, _ := newModel(t)
	text := strings.Repeat("字", pasteLimit) // 3 bytes each, exactly the limit in characters
	update(m, tea.PasteMsg{Content: text})
	if got := m.editor.Value(); got != text {
		t.Fatalf("a paste of %d characters was collapsed: %.40q", pasteLimit, got)
	}
}

func TestUI_EDI33_PasteCountsAsTyping(t *testing.T) {
	m, _, clk := newModel(t)
	update(m, tea.PasteMsg{Content: "pasted draft"})
	clk.now = clk.now.Add(time.Second)
	m.applyEditorText("from an extension")
	if got := m.editor.Value(); got != "pasted draft" {
		t.Fatalf("an extension overwrote a fresh paste: %q", got)
	}
}

func TestUI_EDI34_PastedCommandArguments(t *testing.T) {
	m, f, _ := newModel(t)
	big := strings.Repeat("y", 9000)
	m.setEditorText("/note ")
	update(m, tea.PasteMsg{Content: big})
	do(update(m, enterKey))
	if len(f.sends) != 1 || f.sends[0].Type != proto.CmdRunCommand || f.sends[0].Args != big {
		t.Fatalf("the command received the paste marker instead of the text: %+v", f.sends)
	}
}

func TestUI_EDI21_EditorWithArguments(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("draft")
	t.Setenv("EDITOR", "my-editor --wait --new-window")
	msg := prepared(t, m)
	if msg.err != nil {
		t.Fatalf("err = %v", msg.err)
	}
	want := []string{"my-editor", "--wait", "--new-window", msg.path}
	if !slices.Equal(msg.cmd.Args, want) {
		t.Fatalf("argv = %q, want %q", msg.cmd.Args, want)
	}
	if data, err := os.ReadFile(msg.path); err != nil || string(data) != "draft" {
		t.Fatalf("temp file = %q, %v; want the editor text", data, err)
	}
}

func TestUI_EDI22_BlankEditorVariable(t *testing.T) {
	m, _, _ := newModel(t)
	t.Setenv("EDITOR", "  \t ")
	if msg := prepared(t, m); !errors.Is(msg.err, errNoEditor) {
		t.Fatalf("err = %v, want errNoEditor", msg.err)
	}
}

func TestUI_EDI23_FailedEditorRemovesItsTempFile(t *testing.T) {
	m, _, _ := newModel(t)
	t.Setenv("EDITOR", "my-editor")
	msg := prepared(t, m)
	cmd := m.readEditor(editorFinishedMsg{path: msg.path, err: errors.New("exit status 1")})
	if m.notice == "" {
		t.Fatal("a failed editor set no notice")
	}
	do(cmd)
	if _, err := os.Stat(msg.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file still present after a failed editor: %v", err)
	}
}

func TestUI_EDI24_FinishedEditorReturnsTextAndCleansUp(t *testing.T) {
	m, _, _ := newModel(t)
	t.Setenv("EDITOR", "my-editor")
	msg := prepared(t, m)
	if err := os.WriteFile(msg.path, []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	update(m, do(m.readEditor(editorFinishedMsg{path: msg.path})))
	if got := m.editor.Value(); got != "edited" {
		t.Fatalf("editor = %q, want the file content without its trailing newline", got)
	}
	if _, err := os.Stat(msg.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file still present: %v", err)
	}
}
