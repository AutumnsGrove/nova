//go:build !notui

package ui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/client"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// colorModel builds a truecolor model for splash tests.
func colorModel(t *testing.T, w, h int) *Model {
	t.Helper()
	f := &fakeClient{responses: map[string]proto.Response{}}
	m := New(Options{
		Client: f, Theme: styles.Dark(), Clock: &fakeClock{now: time.Unix(1_700_000_000, 0)},
		CWD: "/home/nova/proj", Profile: colorprofile.TrueColor, Renderer: echoRenderer{},
		Width: w, Height: h,
	})
	m.home = "/home/nova"
	return m
}

func TestUI_THM07_ThemeLists(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, runeKey('/'))
	m.setEditorText("/theme")
	update(m, enterKey)
	if !strings.Contains(m.notice, "nova-dark") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestUI_THM08_ThemeSwitches(t *testing.T) {
	m, _, _ := newModel(t)
	m.transcript.AddUser("hello", nil)
	m.syncList()
	item := m.transcript.Items()[0]
	v := item.Version()
	if !m.setTheme("nova-light") {
		t.Fatal("setTheme failed")
	}
	if m.theme.Name != "nova-light" {
		t.Fatalf("theme = %q", m.theme.Name)
	}
	if item.Version() == v {
		t.Fatal("items were not refreshed")
	}
}

func TestUI_THM09_UnknownTheme(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("/theme nope")
	update(m, enterKey)
	if !strings.Contains(m.notice, "unknown theme") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestUI_DSP01_ThinkingExpand(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("/display thinking expand")
	update(m, enterKey)
	if !m.display.ExpandThinking {
		t.Fatal("thinking not expanded")
	}
}

func TestUI_DSP02_Lines(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("/display lines 12")
	update(m, enterKey)
	if m.display.ThinkingPreviewLines != 12 {
		t.Fatalf("preview lines = %d", m.display.ThinkingPreviewLines)
	}
}

func TestUI_DSP03_LinesZero(t *testing.T) {
	m, _, _ := newModel(t)
	before := m.display.ThinkingPreviewLines
	m.setEditorText("/display lines 0")
	update(m, enterKey)
	if m.display.ThinkingPreviewLines != before {
		t.Fatalf("lines changed to %d", m.display.ThinkingPreviewLines)
	}
	if !strings.Contains(m.notice, "positive number") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestUI_DSP04_SplashOff(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("/display splash off")
	update(m, enterKey)
	if m.display.Splash {
		t.Fatal("splash still on")
	}
}

func TestUI_DSP05_CtrlOTogglesBoth(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, ctrlKey('o'))
	if !m.display.ExpandThinking || !m.display.ExpandTools {
		t.Fatalf("flags = %+v", m.display)
	}
	update(m, ctrlKey('o'))
	if m.display.ExpandThinking || m.display.ExpandTools {
		t.Fatalf("flags not collapsed: %+v", m.display)
	}
}

func TestUI_SPL12_Dismissed(t *testing.T) {
	m, _, _ := newModel(t)
	if !m.showSplash() {
		t.Fatal("splash not shown on an empty transcript")
	}
	update(m, runeKey('a'))
	if m.showSplash() {
		t.Fatal("splash not dismissed by a key")
	}
	m2, _, _ := newModel(t)
	update(m2, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
	if m2.showSplash() {
		t.Fatal("splash not dismissed by an event")
	}
}

func TestUI_SPL13_NotShownWhenNotEmpty(t *testing.T) {
	m, _, _ := newModel(t)
	m.transcript.AddUser("hi", nil)
	m.syncList()
	if m.showSplash() {
		t.Fatal("splash shown with a non-empty transcript")
	}
}

func TestUI_SPL14_ResizeRerenders(t *testing.T) {
	m := colorModel(t, 120, 60)
	m.splashAt = 4 * time.Second // past assembly, so the word is drawn
	out := m.render()
	if !strings.Contains(out, "╱") {
		t.Fatal("large splash has no slash wordmark")
	}
	update(m, tea.WindowSizeMsg{Width: 30, Height: 18})
	small := m.render()
	if !strings.Contains(small, "press any key") {
		t.Fatal("small splash has no plain prompt")
	}
}

// --- GLR goldens ---

func TestUI_GLR01_FullTurn(t *testing.T) {
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		m.transcript.AddUser("add a retry", nil)
		m.transcript.Apply(proto.Event{Type: proto.EventThinkingStart, Run: "r", Turn: intp(1)})
		m.transcript.Apply(proto.Event{Type: proto.EventThinkingDelta, Run: "r", Turn: intp(1), Text: "the call is in client.go"})
		tokens := 3400
		m.transcript.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r", Turn: intp(1), Ms: 12000, Tokens: &tokens})
		m.transcript.Apply(proto.Event{Type: proto.EventToolCall, Run: "r", Call: &proto.ToolCall{ID: "c1", Name: "edit", Arguments: json.RawMessage(`{"path":"client.go"}`)}})
		m.transcript.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c1", Result: &proto.ToolResult{
			Content: "applied 1 edit",
			Details: json.RawMessage(`{"added":9,"removed":2,"diff":"@@ -1,2 +1,2 @@\n-old\n+new"}`),
		}})
		m.syncList()
	}))
}

func TestUI_GLR02_ThinkingStates(t *testing.T) {
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		start := time.Unix(1_700_000_000, 0)
		tr := m.transcript
		turn := 1
		tr.SetNow(start)
		tr.Apply(proto.Event{Type: proto.EventThinkingStart, Run: "r", Turn: &turn})
		tr.Apply(proto.Event{Type: proto.EventThinkingDelta, Run: "r", Turn: &turn, Text: "streaming reasoning"})
		tr.SetNow(start.Add(4 * time.Second))
		tokens := 15000
		tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r", Turn: &turn, Ms: 20000, Tokens: &tokens})
		tr.Apply(proto.Event{Type: proto.EventThinkingStart, Run: "r2", Turn: &turn})
		tr.Apply(proto.Event{Type: proto.EventThinkingDelta, Run: "r2", Turn: &turn, Text: "expanded reasoning body"})
		tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r2", Turn: &turn, Ms: 1000})
		d := tr.Display()
		d.ExpandThinking = true
		tr.SetDisplay(d)
		m.syncList()
	}))
}

func TestUI_GLR03_AllCards(t *testing.T) {
	calls := []struct {
		name    string
		args    string
		content string
		details string
	}{
		{"read", `{"path":"a.go"}`, "1\tpackage main", `{"start":1,"end":1}`},
		{"write", `{"path":"a.go","content":"package main"}`, "wrote 12 bytes", `{"created":true,"bytes":12}`},
		{"edit", `{"path":"a.go"}`, "applied", `{"added":1,"removed":1,"diff":"@@ -1 +1 @@\n-o\n+n"}`},
		{"bash", `{"command":"ls"}`, "file", `{"exit_code":0}`},
		{"glob", `{"pattern":"*.go"}`, "a.go", `{"count":1}`},
		{"grep", `{"pattern":"main"}`, "a.go:1:main", `{"matches":1}`},
		{"list", `{"path":"."}`, "a.go", `{"count":1}`},
		{"mystery", `{"x":"y"}`, "result", ""},
	}
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		for i, c := range calls {
			call := &proto.ToolCall{ID: fmt.Sprintf("c%d", i), Name: c.name, Arguments: json.RawMessage(c.args)}
			m.transcript.Apply(proto.Event{Type: proto.EventToolCall, Run: "r", Call: call})
			details := json.RawMessage(c.details)
			if c.details == "" {
				details = nil
			}
			m.transcript.Apply(proto.Event{Type: proto.EventToolEnd, CallID: call.ID, Result: &proto.ToolResult{Content: c.content, Details: details}})
		}
		m.syncList()
	}))
}

func TestUI_GLR04_Themes(t *testing.T) {
	var b strings.Builder
	for _, th := range []styles.Theme{styles.Dark(), styles.Light(), styles.Mono()} {
		f := &fakeClient{responses: map[string]proto.Response{}}
		m := New(Options{
			Client: f, Theme: th, Clock: &fakeClock{now: time.Unix(1_700_000_000, 0)},
			CWD: "/home/nova/proj", Profile: colorprofile.TrueColor, Renderer: echoRenderer{},
			Width: 80, Height: 24,
		})
		m.home = "/home/nova"
		m.transcript.AddUser("hello", nil)
		m.syncList()
		fmt.Fprintf(&b, "--- %s ---\n%s\n", th.Name, m.render())
	}
	golden.RequireEqual(t, b.String())
}

// BenchmarkUI_PRR01_ToolCards measures a frame over 2,000 tool cards.
func BenchmarkUI_PRR01_ToolCards(b *testing.B) {
	f := &fakeClient{responses: map[string]proto.Response{}}
	m := New(Options{Client: f, Theme: styles.Mono(), Clock: &fakeClock{now: time.Unix(0, 0)}, CWD: "/tmp", Profile: colorprofile.ASCII, Renderer: echoRenderer{}, Width: 80, Height: 40})
	for i := 0; i < 2000; i++ {
		call := &proto.ToolCall{ID: fmt.Sprintf("c%d", i), Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)}
		m.transcript.Apply(proto.Event{Type: proto.EventToolCall, Run: "r", Call: call})
		m.transcript.Apply(proto.Event{Type: proto.EventToolEnd, CallID: call.ID, Result: &proto.ToolResult{Content: "x"}})
	}
	m.syncList()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.list.ScrollBy(i%2*2 - 1)
		_ = m.list.View()
	}
}

// BenchmarkUI_PRR02_Thinking measures a delta into a large thinking block.
func BenchmarkUI_PRR02_Thinking(b *testing.B) {
	f := &fakeClient{responses: map[string]proto.Response{}}
	m := New(Options{Client: f, Theme: styles.Mono(), Clock: &fakeClock{now: time.Unix(0, 0)}, CWD: "/tmp", Profile: colorprofile.ASCII, Renderer: echoRenderer{}, Width: 80, Height: 40})
	turn := 1
	m.transcript.Apply(proto.Event{Type: proto.EventThinkingStart, Run: "r", Turn: &turn})
	big := strings.Repeat("word ", 20_000)
	m.transcript.Apply(proto.Event{Type: proto.EventThinkingDelta, Run: "r", Turn: &turn, Text: big})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.transcript.Apply(proto.Event{Type: proto.EventThinkingDelta, Run: "r", Turn: &turn, Text: "x"})
	}
}

func TestUI_THM10_MonoNoColour(t *testing.T) {
	m, _, _ := newModel(t)
	m.transcript.AddUser("hello", nil)
	m.transcript.Apply(proto.Event{Type: proto.EventToolCall, Run: "r", Call: &proto.ToolCall{ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)}})
	m.transcript.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c1", Result: &proto.ToolResult{Content: "1\tpackage main", Details: json.RawMessage(`{"start":1,"end":1}`)}})
	m.syncList()
	if out := m.render(); hasColourEscape(out) {
		t.Fatalf("mono transcript has colour escapes: %q", out)
	}
}

// hasColour reports whether c is a real colour; an unset lipgloss style reports
// NoColor, which is not.
func hasColour(c color.Color) bool {
	if c == nil {
		return false
	}
	_, none := c.(lipgloss.NoColor)
	return !none
}

// sameColour compares two colours by their rendered RGBA, since the types differ.
func sameColour(a, b color.Color) bool {
	if !hasColour(a) || !hasColour(b) {
		return !hasColour(a) && !hasColour(b)
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func TestUI_THM11_EditorFollowsTheme(t *testing.T) {
	m := colorModel(t, 80, 24)
	check := func(label string) {
		t.Helper()
		got := m.editor.Styles()
		for _, c := range []struct {
			name       string
			have, want color.Color
		}{
			{"prompt", got.Focused.Prompt.GetForeground(), m.st.Spark.GetForeground()},
			{"text", got.Focused.Text.GetForeground(), m.st.Text.GetForeground()},
			{"placeholder", got.Focused.Placeholder.GetForeground(), m.st.Muted.GetForeground()},
			{"cursor", got.Cursor.Color, m.st.Spark.GetForeground()},
			{"blurred text", got.Blurred.Text.GetForeground(), m.st.Muted.GetForeground()},
		} {
			if c.want == nil {
				t.Fatalf("%s: theme has no %s colour; the test would pass vacuously", label, c.name)
			}
			if !sameColour(c.have, c.want) {
				t.Errorf("%s: %s colour = %v, want the theme's %v", label, c.name, c.have, c.want)
			}
		}
		if bg := got.Focused.CursorLine.GetBackground(); hasColour(bg) {
			t.Errorf("%s: cursor line has a background %v; it should not highlight", label, bg)
		}
	}
	check("dark")
	if !m.setTheme("nova-light") {
		t.Fatal("setTheme failed")
	}
	check("light after /theme")
}

func TestUI_THM12_EditorMonoHasNoColour(t *testing.T) {
	m, _, _ := newModel(t) // mono theme, ASCII profile
	got := m.editor.Styles()
	for name, c := range map[string]color.Color{
		"prompt":      got.Focused.Prompt.GetForeground(),
		"text":        got.Focused.Text.GetForeground(),
		"placeholder": got.Focused.Placeholder.GetForeground(),
		"cursor line": got.Focused.CursorLine.GetBackground(),
	} {
		if hasColour(c) {
			t.Errorf("%s has colour %v in the mono theme", name, c)
		}
	}
}
