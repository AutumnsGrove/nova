//go:build !notui

package chat

import (
	"bufio"
	"encoding/json"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/list"
	"github.com/AutumnsGrove/nova/tui/styles"
	"github.com/charmbracelet/colorprofile"
)

func testStyles() styles.Styles { return styles.Build(styles.Mono(), colorprofile.ASCII) }

func newTestTranscript(width int) *Transcript {
	return NewTranscript(width, &fakeRenderer{echo: true}, testStyles())
}

func turnEvent(typ string, turn int) proto.Event {
	return proto.Event{Type: typ, Run: "r1", Turn: &turn}
}

func assistantStart(turn int) proto.Event {
	e := turnEvent(proto.EventMessageStart, turn)
	e.Role = string(message.RoleAssistant)
	return e
}

func plain(s string) string { return ansi.Strip(s) }

func renderItem(it list.Item, width int) string { return plain(it.Render(width)) }

func findTool(tr *Transcript, callID string) *toolItem {
	for _, it := range tr.Items() {
		if ti, ok := it.(*toolItem); ok && ti.call.ID == callID {
			return ti
		}
	}
	return nil
}

func itemIDs(tr *Transcript) []string {
	ids := make([]string, 0, len(tr.Items()))
	for _, it := range tr.Items() {
		ids = append(ids, it.ID())
	}
	return ids
}

func TestTranscript_TRN01_TextTurn(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	tr.Apply(proto.Event{Type: proto.EventTextDelta, Run: "r1", Turn: intp(1), Text: "Hello "})
	tr.Apply(proto.Event{Type: proto.EventTextDelta, Run: "r1", Turn: intp(1), Text: "world"})
	tr.Apply(turnEvent(proto.EventMessageEnd, 1))
	if len(tr.Items()) != 1 {
		t.Fatalf("items = %d, want 1", len(tr.Items()))
	}
	if got := renderItem(tr.Items()[0], 80); !strings.Contains(got, "Hello world") {
		t.Fatalf("assistant render = %q", got)
	}
}

func TestTranscript_CRD15_ToggleOneCard(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	tr.Apply(proto.Event{Type: proto.EventToolCall, Run: "r1", Call: &proto.ToolCall{ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)}})
	tr.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c1", Result: &proto.ToolResult{Content: "one"}})
	tr.Apply(proto.Event{Type: proto.EventToolCall, Run: "r1", Call: &proto.ToolCall{ID: "c2", Name: "read", Arguments: json.RawMessage(`{"path":"b.go"}`)}})
	tr.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c2", Result: &proto.ToolResult{Content: "two"}})
	one, two := findTool(tr, "c1"), findTool(tr, "c2")
	if one == nil || two == nil {
		t.Fatal("tool cards missing")
	}
	if !tr.Toggle(one.id) {
		t.Fatal("Toggle did not find the first card")
	}
	if !one.expanded {
		t.Fatal("first card did not expand")
	}
	if two.expanded {
		t.Fatal("second card expanded too")
	}
	if !tr.Toggle(one.id) || one.expanded {
		t.Fatal("second Toggle did not collapse the card")
	}
}

func TestTranscript_TRN02_ToolTurn(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	tr.Apply(proto.Event{Type: proto.EventToolCall, Run: "r1", Call: &proto.ToolCall{ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)}})
	tr.Apply(proto.Event{Type: proto.EventToolStart, CallID: "c1"})
	tr.Apply(proto.Event{Type: proto.EventToolUpdate, CallID: "c1", Text: "partial"})
	tr.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c1", Result: &proto.ToolResult{Content: "file contents"}})
	tool := findTool(tr, "c1")
	if tool == nil {
		t.Fatal("tool item missing")
	}
	if tool.call.Running {
		t.Fatal("tool still marked running")
	}
	if tool.call.Content != "file contents" {
		t.Fatalf("tool result = %q", tool.call.Content)
	}
	if got := renderItem(tool, 80); !strings.Contains(got, "read") {
		t.Fatalf("tool render = %q", got)
	}
}

func TestTranscript_TRN03_ToolOrder(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	for _, c := range []string{"c1", "c2", "c3"} {
		tr.Apply(proto.Event{Type: proto.EventToolCall, Run: "r1", Call: &proto.ToolCall{ID: c, Name: "tool" + c}})
	}
	var got []string
	for _, it := range tr.Items() {
		if ti, ok := it.(*toolItem); ok {
			got = append(got, ti.call.ID)
		}
	}
	if strings.Join(got, ",") != "c1,c2,c3" {
		t.Fatalf("tool order = %v", got)
	}
}

func TestTranscript_TRN04_ToolError(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(proto.Event{Type: proto.EventToolCall, Run: "r1", Call: &proto.ToolCall{ID: "c1", Name: "bash"}})
	tr.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c1", Result: &proto.ToolResult{Content: "boom", IsError: true}})
	tool := findTool(tr, "c1")
	if tool == nil || !tool.call.IsError {
		t.Fatalf("tool error not marked: %+v", tool)
	}
}

func TestTranscript_TRN05_UnknownCallIDIgnored(t *testing.T) {
	tr := newTestTranscript(80)
	before := len(tr.Items())
	changed := tr.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "nope", Result: &proto.ToolResult{Content: "x"}})
	if len(tr.Items()) != before || len(changed) != 0 {
		t.Fatalf("unknown call id was not ignored: items=%d changed=%v", len(tr.Items()), changed)
	}
}

func TestTranscript_TRN06_RetryDiscardsPartial(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	tr.Apply(proto.Event{Type: proto.EventTextDelta, Run: "r1", Turn: intp(1), Text: "partial answer"})
	tr.Apply(proto.Event{Type: proto.EventRetry, Run: "r1", Turn: intp(1), Attempt: 1, Max: 3})
	for _, it := range tr.Items() {
		if _, ok := it.(*assistantItem); ok {
			t.Fatal("partial assistant item was not discarded")
		}
	}
	found := false
	for _, it := range tr.Items() {
		if strings.Contains(renderItem(it, 80), "retrying (1/3)") {
			found = true
		}
	}
	if !found {
		t.Fatal("retry line missing")
	}
}

func TestTranscript_TRN07_CompactionMarker(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(proto.Event{Type: proto.EventCompaction, TokensBefore: 38000, TokensAfter: 6000})
	if got := renderItem(tr.Items()[0], 80); !strings.Contains(got, "compacted") {
		t.Fatalf("compaction render = %q", got)
	}
}

func TestTranscript_TRN08_ErrorEvent(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(proto.Event{Type: proto.EventError, Text: "provider exploded"})
	if got := renderItem(tr.Items()[0], 80); !strings.Contains(got, "provider exploded") {
		t.Fatalf("error render = %q", got)
	}
}

// TRN09: run_end leaves a line for every reason except a normal end, so a run
// never stops silently.
func TestTranscript_TRN09_RunEndReasons(t *testing.T) {
	cases := []struct {
		reason   string
		turn     *int
		err      *proto.Error
		want     string
		wantItem bool
	}{
		{reason: "done", wantItem: false},
		{reason: "aborted", want: "aborted", wantItem: true},
		{reason: "error", err: &proto.Error{Code: proto.CodeInternal, Message: "boom"}, want: "error", wantItem: true},
		{reason: "exhausted", turn: intp(30), want: "turn budget", wantItem: true},
		{reason: "future", want: "future", wantItem: true},
	}
	for _, c := range cases {
		tr := newTestTranscript(80)
		tr.Apply(proto.Event{Type: proto.EventRunEnd, Reason: c.reason, Turn: c.turn, Error: c.err})
		items := tr.Items()
		if !c.wantItem {
			if len(items) != 0 {
				t.Fatalf("%s: items = %d, want 0 (silent)", c.reason, len(items))
			}
			continue
		}
		if len(items) == 0 {
			t.Fatalf("%s: no closing line", c.reason)
		}
		if got := renderItem(items[0], 80); !strings.Contains(got, c.want) {
			t.Fatalf("%s: render = %q, want it to contain %q", c.reason, got, c.want)
		}
	}
}

// PNL04: a subagent card is a tool card like any other, so it carries the same
// state-tinted panel and padding as the built-in tools.
func TestTranscript_PNL04_SubagentPanelled(t *testing.T) {
	st := styles.Build(styles.Dark(), colorprofile.TrueColor)
	tr := NewTranscript(80, &fakeRenderer{echo: true}, st)
	tr.Apply(proto.Event{Type: proto.EventToolCall, Call: &proto.ToolCall{
		ID: "c1", Name: "subagent", Arguments: json.RawMessage(`{"task":"do it"}`),
	}})
	tr.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c1", Result: &proto.ToolResult{Content: "done"}})
	items := tr.Items()
	if len(items) == 0 {
		t.Fatal("no item for the subagent call")
	}
	out := items[0].Render(80)
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, "48;") {
			t.Fatalf("subagent card line has no panel background: %q", l)
		}
		if w := ansi.StringWidth(l); w != 80 {
			t.Fatalf("subagent card line is %d wide, want 80", w)
		}
	}
}

func TestTranscript_TRN10_NeverReorders(t *testing.T) {
	rng := rand.New(rand.NewSource(10))
	tr := newTestTranscript(80)
	for i := 0; i < 2000; i++ {
		before := itemIDs(tr)
		tr.Apply(randomEvent(rng, i%7))
		after := itemIDs(tr)
		if !subsequence(before, after) {
			t.Fatalf("items reordered at step %d:\n before %v\n after  %v", i, before, after)
		}
	}
}

func TestTranscript_TRN11_LoadStoredMessages(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Load([]message.Message{
		message.User("hi"),
		{
			Role:    message.RoleAssistant,
			Content: "let me read",
			ToolCalls: []message.ToolCall{{
				ID:       "c1",
				Function: message.FunctionCall{Name: "read", Arguments: `{"path":"x"}`},
			}},
		},
		message.ToolResult("c1", "file data"),
		message.Assistant("done"),
	})
	if len(tr.Items()) != 4 {
		t.Fatalf("loaded %d items, want 4", len(tr.Items()))
	}
	tool := findTool(tr, "c1")
	if tool == nil || tool.call.Content != "file data" {
		t.Fatalf("tool result not attached: %+v", tool)
	}
}

func TestTranscript_TRN12_LoadOrphanToolResult(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Load([]message.Message{message.ToolResult("missing", "orphan data")})
	if len(tr.Items()) != 1 {
		t.Fatalf("items = %d, want 1", len(tr.Items()))
	}
	if got := renderItem(tr.Items()[0], 80); !strings.Contains(got, "orphan data") {
		t.Fatalf("orphan note = %q", got)
	}
}

func TestTranscript_TRN13_ThinkingStoredNotRendered(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	tr.Apply(proto.Event{Type: proto.EventThinkingStart, Run: "r1", Turn: intp(1)})
	tr.Apply(proto.Event{Type: proto.EventThinkingDelta, Run: "r1", Turn: intp(1), Text: "secret thoughts"})
	tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(1)})
	th := tr.thinks[tr.key(proto.Event{Run: "r1", Turn: intp(1)})]
	if th == nil || th.text != "secret thoughts" {
		t.Fatalf("thinking not stored: %+v", th)
	}
	// The thinking text lives in its own item, never in the assistant text.
	for _, it := range tr.Items() {
		if a, ok := it.(*assistantItem); ok && strings.Contains(renderItem(a, 80), "secret thoughts") {
			t.Fatalf("thinking leaked into the assistant item")
		}
	}
}

func TestTranscript_TRN14_UserPromptAppearsAfterAccept(t *testing.T) {
	tr := newTestTranscript(80)
	if len(tr.Items()) != 0 {
		t.Fatal("transcript is not empty before the response")
	}
	tr.AddUser("what does this do?", []string{"main.go"})
	if len(tr.Items()) != 1 {
		t.Fatal("user item was not added")
	}
	if got := renderItem(tr.Items()[0], 80); !strings.Contains(got, "what does this do?") || !strings.Contains(got, "main.go") {
		t.Fatalf("user render = %q", got)
	}
}

func TestTranscript_TRN15_QueuedThenNormal(t *testing.T) {
	tr := newTestTranscript(80)
	tr.QueueUser("steer this", "steer")
	if got := renderItem(tr.Items()[0], 80); !strings.Contains(got, "queued") {
		t.Fatalf("queued message not marked: %q", got)
	}
	steering := 0
	tr.Apply(proto.Event{Type: proto.EventQueueUpdate, Steering: &steering})
	if got := renderItem(tr.Items()[0], 80); strings.Contains(got, "queued") {
		t.Fatalf("consumed message still marked queued: %q", got)
	}
}

func TestTranscript_TRN16_ReplayFixture(t *testing.T) {
	tr := newTestTranscript(80)
	f, err := os.Open("testdata/events/basic.jsonl")
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
		tr.Apply(e)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range tr.Items() {
		out = append(out, it.Render(80))
	}
	golden.RequireEqual(t, strings.Join(out, "\n"))
}

func TestTranscript_TRN17_SetWidth(t *testing.T) {
	tr := newTestTranscript(40)
	tr.Apply(assistantStart(1))
	tr.Apply(proto.Event{Type: proto.EventTextDelta, Run: "r1", Turn: intp(1), Text: strings.Repeat("word ", 30)})
	item := tr.Items()[0]
	wide := item.Render(40)
	tr.SetWidth(20)
	if tr.Items()[0] != item {
		t.Fatal("SetWidth recreated the item")
	}
	narrow := item.Render(20)
	for _, line := range strings.Split(narrow, "\n") {
		if w := ansi.StringWidth(line); w > 20 {
			t.Fatalf("line %q is %d cells after SetWidth", line, w)
		}
	}
	if wide == narrow {
		t.Fatal("render did not change with width")
	}
}

// FuzzTranscriptApply asserts arbitrary event streams never panic and never
// reorder existing items.
func FuzzTranscriptApply(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	f.Fuzz(func(t *testing.T, data []byte) {
		tr := newTestTranscript(40)
		before := itemIDs(tr)
		for _, b := range data {
			tr.Apply(randomEvent(rand.New(rand.NewSource(int64(b))), int(b)))
			after := itemIDs(tr)
			if !subsequence(before, after) {
				t.Fatalf("items reordered:\n before %v\n after  %v", before, after)
			}
			before = after
		}
	})
}

// randomEvent builds a valid event from a small choice so property tests can
// generate streams without constructing every field.
func randomEvent(rng *rand.Rand, n int) proto.Event {
	turn := n%3 + 1
	call := "c" + strings.Repeat("x", n%3+1)
	switch n % 12 {
	case 0:
		return assistantStart(turn)
	case 1:
		return proto.Event{Type: proto.EventTextDelta, Run: "r1", Turn: intp(turn), Text: "text "}
	case 2:
		return proto.Event{Type: proto.EventToolCall, Run: "r1", Call: &proto.ToolCall{ID: call, Name: "read", Arguments: json.RawMessage(`{"path":"x"}`)}}
	case 3:
		return proto.Event{Type: proto.EventToolStart, CallID: call}
	case 4:
		return proto.Event{Type: proto.EventToolUpdate, CallID: call, Text: "out"}
	case 5:
		return proto.Event{Type: proto.EventToolEnd, CallID: call, Result: &proto.ToolResult{Content: "result"}}
	case 6:
		return turnEvent(proto.EventMessageEnd, turn)
	case 7:
		return proto.Event{Type: proto.EventThinkingDelta, Run: "r1", Turn: intp(turn), Text: "hmm"}
	case 8:
		return proto.Event{Type: proto.EventRetry, Run: "r1", Turn: intp(turn), Attempt: 1, Max: 3}
	case 9:
		return proto.Event{Type: proto.EventCompaction, TokensBefore: 10, TokensAfter: 5}
	case 10:
		return proto.Event{Type: proto.EventToolEnd, CallID: "unknown", Result: &proto.ToolResult{Content: "x"}}
	default:
		return proto.Event{Type: proto.EventError, Text: "err"}
	}
}

func intp(v int) *int { return &v }

// subsequence reports whether the ids still present keep their relative order.
func subsequence(before, after []string) bool {
	present := map[string]bool{}
	for _, id := range after {
		present[id] = true
	}
	i := 0
	for _, id := range before {
		if !present[id] {
			continue
		}
		if i >= len(after) || after[i] != id {
			return false
		}
		i++
	}
	return true
}

func TestTranscript_CRD13_RestoredMatchesLive(t *testing.T) {
	live := newTestTranscript(80)
	live.Apply(proto.Event{Type: proto.EventToolCall, Run: "r1", Call: &proto.ToolCall{ID: "c1", Name: "mystery", Arguments: json.RawMessage(`{"x":"y"}`)}})
	live.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c1", Result: &proto.ToolResult{Content: "result"}})

	restored := newTestTranscript(80)
	restored.Load([]message.Message{
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "c1", Function: message.FunctionCall{Name: "mystery", Arguments: `{"x":"y"}`}}}},
		message.ToolResult("c1", "result"),
	})
	liveTool, restoredTool := findTool(live, "c1"), findTool(restored, "c1")
	if liveTool == nil || restoredTool == nil {
		t.Fatal("tool card missing")
	}
	if renderItem(liveTool, 80) != renderItem(restoredTool, 80) {
		t.Fatalf("live %q != restored %q", renderItem(liveTool, 80), renderItem(restoredTool, 80))
	}
}
