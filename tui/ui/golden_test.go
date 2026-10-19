//go:build !notui

package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// goldModel builds a model with a fixed working directory and home so golden
// views are reproducible.
func goldModel(t *testing.T, width, height int) *Model {
	t.Helper()
	f := &fakeClient{responses: map[string]proto.Response{}}
	m := New(Options{
		Client: f, Theme: styles.Mono(), Clock: &fakeClock{now: time.Unix(1_700_000_000, 0)},
		CWD: "/home/nova/proj", Profile: colorprofile.ASCII, Renderer: echoRenderer{},
		Width: width, Height: height,
	})
	m.home = "/home/nova"
	return m
}

// goldAt renders a model at three widths and joins the views for one golden.
// The splash has finished, so transcript goldens show the settled screen.
func goldAt(t *testing.T, build func(m *Model)) string {
	t.Helper()
	return goldAtSplash(t, false, build)
}

// goldAtBanner renders with the splash banner still playing.
func goldAtBanner(t *testing.T, build func(m *Model)) string {
	t.Helper()
	return goldAtSplash(t, true, build)
}

func goldAtSplash(t *testing.T, banner bool, build func(m *Model)) string {
	t.Helper()
	var b strings.Builder
	for _, w := range []int{40, 80, 120} {
		m := goldModel(t, w, 24)
		if !banner {
			m.splashDone = true
			m.recompute()
		}
		if build != nil {
			build(m)
		}
		fmt.Fprintf(&b, "--- width %d ---\n%s\n", w, m.render())
	}
	return b.String()
}

func TestUI_GLD01_EmptySession(t *testing.T) {
	golden.RequireEqual(t, goldAtBanner(t, nil))
}

func TestUI_GLD02_TextTurn(t *testing.T) {
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		m.transcript.AddUser("add a retry", nil)
		m.transcript.Apply(proto.Event{Type: proto.EventMessageStart, Run: "r", Turn: intp(1), Role: "assistant"})
		m.transcript.Apply(proto.Event{Type: proto.EventTextDelta, Run: "r", Turn: intp(1), Text: "I added a retry to the fetch call."})
		m.transcript.Apply(proto.Event{Type: proto.EventMessageEnd, Run: "r", Turn: intp(1)})
		m.syncList()
	}))
}

func TestUI_GLD03_ToolTurn(t *testing.T) {
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		m.transcript.Apply(proto.Event{Type: proto.EventToolCall, Run: "r", Call: &proto.ToolCall{ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"client.go"}`)}})
		m.transcript.Apply(proto.Event{Type: proto.EventToolStart, CallID: "c1"})
		m.transcript.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c1", Result: &proto.ToolResult{Content: "package client"}})
		m.syncList()
	}))
}

func TestUI_GLD04_StreamingMidMessage(t *testing.T) {
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		m.transcript.Apply(proto.Event{Type: proto.EventMessageStart, Run: "r", Turn: intp(1), Role: "assistant"})
		m.transcript.Apply(proto.Event{Type: proto.EventTextDelta, Run: "r", Turn: intp(1), Text: "partial answer"})
		m.syncList()
	}))
}

func TestUI_GLD05_CompletionPopup(t *testing.T) {
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		m.commands = []commandInfo{{Name: "model"}, {Name: "help"}, {Name: "compact"}}
		m.openCommands("")
	}))
}

func TestUI_GLD06_Footer(t *testing.T) {
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		m.state.EffectiveLimit = 1000
		m.state.ContextTokens = 420
		m.state.Usage.CostUSD = 0.031
		m.state.Steering = 2
	}))
}

func TestUI_GLD07_NoColorView(t *testing.T) {
	out := goldAt(t, func(m *Model) {
		m.transcript.AddUser("hello", nil)
		m.syncList()
	})
	if hasColourEscape(out) {
		t.Fatalf("NO_COLOR view has colour escapes: %q", out)
	}
	golden.RequireEqual(t, out)
}

// hasColourEscape reports whether s contains a colour SGR sequence. Bold,
// dim, and reverse are allowed; foreground and background colours are not.
func hasColourEscape(s string) bool {
	for _, marker := range []string{"38;", "48;", "\x1b[3", "\x1b[4", "\x1b[9"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func TestUI_GLD09_TallEditor(t *testing.T) {
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		var lines []string
		for i := 1; i <= 12; i++ {
			lines = append(lines, fmt.Sprintf("line %d", i))
		}
		m.setEditorText(strings.Join(lines, "\n"))
	}))
}

func TestUI_GLD10_BannerWithTypedCharacter(t *testing.T) {
	golden.RequireEqual(t, goldAtBanner(t, func(m *Model) {
		m.setEditorText("h")
	}))
}

func TestUI_GLD08_RecordedReplay(t *testing.T) {
	out := goldAt(t, func(m *Model) {
		f, err := os.Open("../chat/testdata/events/basic.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var e proto.Event
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("decode %q: %v", line, err)
			}
			m.transcript.Apply(e)
		}
		m.syncList()
	})
	golden.RequireEqual(t, out)
}

// BenchmarkUI_PRF01_Scroll measures a frame of scrolling a large transcript.
func BenchmarkUI_PRF01_Scroll(b *testing.B) {
	f := &fakeClient{responses: map[string]proto.Response{}}
	m := New(Options{Client: f, Theme: styles.Mono(), Clock: &fakeClock{now: time.Unix(0, 0)}, CWD: "/tmp", Profile: colorprofile.ASCII, Renderer: echoRenderer{}, Width: 80, Height: 40})
	for i := 0; i < 10000; i++ {
		m.transcript.AddUser("row", nil)
	}
	m.syncList()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.list.ScrollBy(i%2*2 - 1)
		_ = m.list.View()
	}
}

// BenchmarkUI_PRF02_Deltas measures render work per streamed delta.
func BenchmarkUI_PRF02_Deltas(b *testing.B) {
	f := &fakeClient{responses: map[string]proto.Response{}}
	m := New(Options{Client: f, Theme: styles.Mono(), Clock: &fakeClock{now: time.Unix(0, 0)}, CWD: "/tmp", Profile: colorprofile.ASCII, Renderer: echoRenderer{}, Width: 80, Height: 40})
	turn := 1
	m.transcript.Apply(proto.Event{Type: proto.EventMessageStart, Run: "r", Turn: &turn, Role: "assistant"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.transcript.Apply(proto.Event{Type: proto.EventTextDelta, Run: "r", Turn: &turn, Text: "x"})
	}
}

// TestPTY_PTY01_StartsAndQuits and TestPTY_PTY02_PromptRoundTrip live in
// pty_test.go with the real pseudo-terminal harness.
