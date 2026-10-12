//go:build !notui

package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tui/render"
)

// rawJSON is a small helper for tool arguments in tests.
func rawJSON(s string) json.RawMessage { return json.RawMessage(s) }

// formatDurationForTest and formatTokensForTest expose the shared formatters.
func formatDurationForTest(d time.Duration) string { return render.FormatDuration(d) }
func formatTokensForTest(n int) string             { return render.FormatTokens(n) }

// think returns the only thinking item in a transcript.
func think(t *testing.T, tr *Transcript) *thinkingItem {
	t.Helper()
	for _, it := range tr.Items() {
		if th, ok := it.(*thinkingItem); ok {
			return th
		}
	}
	return nil
}

func startThinking(tr *Transcript, text string) {
	turn := 1
	tr.Apply(proto.Event{Type: proto.EventThinkingStart, Run: "r1", Turn: &turn})
	tr.Apply(proto.Event{Type: proto.EventThinkingDelta, Run: "r1", Turn: &turn, Text: text})
}

func TestThinking_THK01_CreatedAboveText(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	startThinking(tr, "reason")
	tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(1)})
	th, a := -1, -1
	for i, it := range tr.Items() {
		switch it.(type) {
		case *thinkingItem:
			th = i
		case *assistantItem:
			a = i
		}
	}
	if th < 0 || a < 0 || th > a {
		t.Fatalf("thinking index %d, assistant index %d", th, a)
	}
}

func TestThinking_THK02_CreatedOnDelta(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(proto.Event{Type: proto.EventThinkingDelta, Run: "r1", Turn: intp(1), Text: "no start"})
	if think(t, tr) == nil {
		t.Fatal("thinking item was not created")
	}
}

func TestThinking_THK03_NoneWithoutReasoning(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	tr.Apply(proto.Event{Type: proto.EventTextDelta, Run: "r1", Turn: intp(1), Text: "hi"})
	if think(t, tr) != nil {
		t.Fatal("thinking item created without reasoning")
	}
}

func TestThinking_THK04_StreamingTimer(t *testing.T) {
	tr := newTestTranscript(80)
	start := time.Unix(0, 0)
	tr.SetNow(start)
	startThinking(tr, "reason")
	tr.SetNow(start.Add(4 * time.Second))
	if got := renderItem(think(t, tr), 80); !strings.Contains(got, "Thinking for 4s") {
		t.Fatalf("streaming header = %q", got)
	}
}

func TestThinking_THK05_TickOnlyWhileStreaming(t *testing.T) {
	tr := newTestTranscript(80)
	startThinking(tr, "reason")
	if !tr.NeedsTick() {
		t.Fatal("no tick while streaming")
	}
	tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(1)})
	if tr.NeedsTick() {
		t.Fatal("tick still needed after thinking_end")
	}
}

func TestThinking_THK06_PreviewTail(t *testing.T) {
	tr := newTestTranscript(40)
	startThinking(tr, strings.Repeat("line\n", 30)+"last")
	got := renderItem(think(t, tr), 40)
	if !strings.Contains(got, "…") || !strings.Contains(got, "last") {
		t.Fatalf("preview = %q", got)
	}
}

func TestThinking_THK07_ShortPreview(t *testing.T) {
	tr := newTestTranscript(40)
	startThinking(tr, "one\ntwo\nthree")
	got := renderItem(think(t, tr), 40)
	if strings.Contains(got, "…") {
		t.Fatalf("unexpected ellipsis: %q", got)
	}
}

func TestThinking_THK08_VersionStableWithinSecond(t *testing.T) {
	tr := newTestTranscript(80)
	start := time.Unix(0, 0)
	tr.SetNow(start)
	startThinking(tr, "reason")
	th := think(t, tr)
	v := th.Version()
	if changed := tr.SetNow(start.Add(500 * time.Millisecond)); len(changed) != 0 {
		t.Fatal("tick within one second re-rendered")
	}
	if th.Version() != v {
		t.Fatal("version changed within one second")
	}
	if changed := tr.SetNow(start.Add(1500 * time.Millisecond)); len(changed) == 0 {
		t.Fatal("tick past one second did not re-render")
	}
}

func TestThinking_THK09_FinishedLabel(t *testing.T) {
	tr := newTestTranscript(120)
	startThinking(tr, "reason")
	tokens := 3400
	tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(1), Ms: 12000, Tokens: &tokens})
	got := renderItem(think(t, tr), 120)
	if !strings.Contains(got, "Thought for 12s · 3.4k tokens") {
		t.Fatalf("finished label = %q", got)
	}
}

func TestThinking_THK10_TokensFromUsage(t *testing.T) {
	tr := newTestTranscript(120)
	startThinking(tr, "reason")
	tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(1), Ms: 1000})
	tr.Apply(proto.Event{Type: proto.EventMessageEnd, Run: "r1", Turn: intp(1), Usage: &provider.Usage{ReasoningTokens: 5000}})
	if got := renderItem(think(t, tr), 120); !strings.Contains(got, "5k tokens") {
		t.Fatalf("usage tokens missing: %q", got)
	}
}

func TestThinking_THK11_EstimatedTokens(t *testing.T) {
	tr := newTestTranscript(120)
	startThinking(tr, strings.Repeat("x", 400))
	tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(1), Ms: 1000})
	if got := renderItem(think(t, tr), 120); !strings.Contains(got, "~") {
		t.Fatalf("estimate marker missing: %q", got)
	}
}

func TestThinking_THK12_DurationFormats(t *testing.T) {
	cases := map[time.Duration]string{
		850 * time.Millisecond: "850ms",
		4 * time.Second:        "4s",
		65 * time.Second:       "1m 05s",
	}
	for d, want := range cases {
		if got := formatDurationForTest(d); got != want {
			t.Fatalf("duration %v = %q, want %q", d, got, want)
		}
	}
}

func TestThinking_THK13_TokenFormats(t *testing.T) {
	cases := map[int]string{640: "640", 3400: "3.4k", 15000: "15k", 1200000: "1.2M"}
	for n, want := range cases {
		if got := formatTokensForTest(n); got != want {
			t.Fatalf("tokens %d = %q, want %q", n, got, want)
		}
	}
}

func TestThinking_THK14_Hint(t *testing.T) {
	tr := newTestTranscript(120)
	startThinking(tr, "reason")
	tokens := 3400
	tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(1), Ms: 12000, Tokens: &tokens})
	if got := renderItem(think(t, tr), 120); !strings.Contains(got, "ctrl+o to expand") {
		t.Fatalf("wide hint missing: %q", got)
	}
	if got := renderItem(think(t, tr), 40); strings.Contains(got, "ctrl+o to expand") {
		t.Fatalf("narrow hint should be dropped: %q", got)
	}
}

func TestThinking_THK15_Expanded(t *testing.T) {
	tr := newTestTranscript(80)
	startThinking(tr, "the whole reasoning text")
	tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(1), Ms: 1000})
	d := tr.Display()
	d.ExpandThinking = true
	tr.SetDisplay(d)
	if got := renderItem(think(t, tr), 80); !strings.Contains(got, "the whole reasoning text") {
		t.Fatalf("expanded text missing: %q", got)
	}
}

func TestThinking_THK16_ExpandToggles(t *testing.T) {
	tr := newTestTranscript(80)
	startThinking(tr, "reason")
	tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(1)})
	th := think(t, tr)
	v := th.Version()
	d := tr.Display()
	d.ExpandThinking = true
	tr.SetDisplay(d)
	if th.Version() == v {
		t.Fatal("SetDisplay did not refresh the block")
	}
}

func TestThinking_THK17_EmptyText(t *testing.T) {
	tr := newTestTranscript(80)
	startThinking(tr, "   ")
	if got := renderItem(think(t, tr), 80); got != "" {
		t.Fatalf("empty block rendered %q", got)
	}
}

func TestThinking_THK18_ControlSequences(t *testing.T) {
	tr := newTestTranscript(80)
	startThinking(tr, "a\x1b[2Jb")
	if got := renderItem(think(t, tr), 80); strings.Contains(got, "\x1b[2J") {
		t.Fatalf("control sequence survived: %q", got)
	}
}

func TestThinking_THK19_RetryDiscards(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	startThinking(tr, "partial")
	tr.Apply(proto.Event{Type: proto.EventRetry, Run: "r1", Turn: intp(1), Attempt: 1, Max: 3})
	if think(t, tr) != nil {
		t.Fatal("retry did not discard the thinking block")
	}
}

func TestThinking_THK20_PreviewClamp(t *testing.T) {
	d := Display{ThinkingPreviewLines: 0}
	if d.previewLines() != 1 {
		t.Fatalf("clamp low = %d", d.previewLines())
	}
	d.ThinkingPreviewLines = 99
	if d.previewLines() != 50 {
		t.Fatalf("clamp high = %d", d.previewLines())
	}
}

func TestThinking_THK21_ToolTurn(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Apply(assistantStart(1))
	startThinking(tr, "reason")
	tr.Apply(proto.Event{Type: proto.EventToolCall, Run: "r1", Turn: intp(1), Call: &proto.ToolCall{ID: "c1", Name: "read", Arguments: rawJSON(`{"path":"a.go"}`)}})
	th, tool := -1, -1
	for i, it := range tr.Items() {
		switch it.(type) {
		case *thinkingItem:
			th = i
		case *toolItem:
			tool = i
		}
	}
	if th < 0 || tool < 0 || th > tool {
		t.Fatalf("order: thinking %d tool %d", th, tool)
	}
}

func TestThinking_THK22_TwoTurns(t *testing.T) {
	tr := newTestTranscript(80)
	for turn := 1; turn <= 2; turn++ {
		tr.Apply(assistantStart(turn))
		tr.Apply(proto.Event{Type: proto.EventThinkingStart, Run: "r1", Turn: intp(turn)})
		tr.Apply(proto.Event{Type: proto.EventThinkingDelta, Run: "r1", Turn: intp(turn), Text: "r"})
		tr.Apply(proto.Event{Type: proto.EventThinkingEnd, Run: "r1", Turn: intp(turn), Ms: 1000})
	}
	n := 0
	for _, it := range tr.Items() {
		if _, ok := it.(*thinkingItem); ok {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("thinking blocks = %d, want 2", n)
	}
}

func TestThinking_THK23_NoStoredReasoning(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Load([]message.Message{{Role: message.RoleAssistant, Content: "hi"}})
	if think(t, tr) != nil {
		t.Fatal("thinking created without stored reasoning")
	}
}

func TestThinking_THK24_StoredReasoning(t *testing.T) {
	tr := newTestTranscript(80)
	tr.Load([]message.Message{{Role: message.RoleAssistant, Content: "hi", Reasoning: "stored"}})
	th := think(t, tr)
	if th == nil || th.text != "stored" || !th.done {
		t.Fatalf("stored reasoning missing: %+v", th)
	}
	if got := renderItem(th, 80); strings.Contains(got, " for ") {
		t.Fatalf("stored reasoning should have no duration: %q", got)
	}
}

// FuzzThinkingFormat asserts the formatters never panic and never return "".
func FuzzThinkingFormat(f *testing.F) {
	f.Add(int64(850), 3400)
	f.Add(int64(65_000), 1_200_000)
	f.Fuzz(func(t *testing.T, ms int64, tokens int) {
		if formatDurationForTest(time.Duration(ms)*time.Millisecond) == "" {
			t.Fatal("empty duration")
		}
		if formatTokensForTest(tokens) == "" {
			t.Fatal("empty tokens")
		}
	})
}
