package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

func Test_LPE01_TextOnlyRun(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hi", Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 2}})
	res, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock}, nil, message.User("hi"))
	if res.Answer != "hi" {
		t.Fatalf("answer = %q, want hi", res.Answer)
	}
	want := []string{
		proto.EventRunStart, proto.EventTurnStart, proto.EventMessageStart,
		proto.EventTextDelta, proto.EventMessageEnd, proto.EventUsage,
		proto.EventTurnEnd, proto.EventRunEnd,
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("event order = %v, want %v", got, want)
	}
	if res.Reason != "done" {
		t.Fatalf("reason = %q, want done", res.Reason)
	}
}

func Test_LPE02_ExactlyOneRunStartAndEnd(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	scenarios := []struct {
		name  string
		cfg   func() (Config, []message.Message)
		turns []testkit.Turn
	}{
		{"text", func() (Config, []message.Message) { return Config{}, nil }, []testkit.Turn{{Text: "hi"}}},
		{"tool", func() (Config, []message.Message) { return Config{}, nil }, []testkit.Turn{{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}}, {Text: "done"}}},
		{"exhausted", func() (Config, []message.Message) { return Config{MaxTurns: 1}, nil }, alwaysTool(1)},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			cfg, history := sc.cfg()
			cfg.Provider = testkit.NewProvider(clock, sc.turns...)
			cfg.Model = "m"
			cfg.Clock = clock
			cfg.Tools = registry(echoTool())
			_, events := runCollect(t, cfg, history, message.User("go"))
			assertRunBoundaries(t, events)
		})
	}
}

func Test_LPE03_NothingAfterRunEnd(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hi"})
	_, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock}, nil, message.User("go"))
	assertRunBoundaries(t, events)
}

func Test_LPE04_SeqIncreasesAndRunConstant(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "one"}, testkit.Turn{Text: "two"})
	c := &collector{}
	a := New(Config{Provider: p, Model: "m", Clock: clock, Emit: c.emit})
	for _, prompt := range []string{"a", "b"} {
		if _, err := a.Run(context.Background(), nil, message.User(prompt)); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	var lastSeq int64
	var starts []string
	for _, e := range c.all() {
		if e.Seq <= lastSeq {
			t.Fatalf("seq %d not greater than %d", e.Seq, lastSeq)
		}
		lastSeq = e.Seq
		if e.Type == proto.EventRunStart {
			starts = append(starts, e.Run)
		}
	}
	if len(starts) != 2 || starts[0] == starts[1] {
		t.Fatalf("run ids = %v, want two distinct", starts)
	}
}

func Test_LPE05_TSFromClock(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hi"})
	_, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock}, nil, message.User("go"))
	for _, e := range events {
		if !e.TS.Equal(baseTime) {
			t.Fatalf("event %s TS = %v, want %v", e.Type, e.TS, baseTime)
		}
	}
}

func Test_LPE06_ToolTurnEventOrder(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	_, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	idx := indexOf(t, events)
	if idx[proto.EventToolCall] > idx[proto.EventMessageEnd] {
		t.Fatalf("tool_call after message_end")
	}
	if idx[proto.EventToolStart] > idx[proto.EventToolEnd] {
		t.Fatalf("tool_start after tool_end")
	}
	if idx[proto.EventTurnEnd] < idx[proto.EventToolEnd] {
		t.Fatalf("turn_end before tool_end")
	}
	for _, e := range events {
		if e.Type == proto.EventRunEnd {
			break
		}
		if e.Type == proto.EventTurnEnd && e.Turn == nil {
			t.Fatalf("turn_end has no turn")
		}
	}
}

// indexOf records the first index of each event type.
func indexOf(t *testing.T, events []proto.Event) map[string]int {
	t.Helper()
	out := map[string]int{}
	for i, e := range events {
		if _, ok := out[e.Type]; !ok {
			out[e.Type] = i
		}
	}
	return out
}

func Test_LPE07_ThinkingBeforeText(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Reasoning: "why", Text: "answer"})
	_, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock}, nil, message.User("go"))
	idx := indexOf(t, events)
	if !(idx[proto.EventThinkingStart] < idx[proto.EventThinkingDelta] &&
		idx[proto.EventThinkingDelta] < idx[proto.EventThinkingEnd] &&
		idx[proto.EventThinkingEnd] < idx[proto.EventTextDelta]) {
		t.Fatalf("thinking order wrong: %v", eventTypes(events))
	}
}

func Test_LPE08_ThinkingBeforeToolCall(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{Reasoning: "why", ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	_, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	idx := indexOf(t, events)
	if idx[proto.EventThinkingEnd] > idx[proto.EventToolCall] {
		t.Fatalf("thinking_end after tool_call: %v", eventTypes(events))
	}
}

func Test_LPE09_ThinkingDuration(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := &advancingProvider{clock: clock, text: "answer"}
	_, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock}, nil, message.User("go"))
	end, ok := findType(events, proto.EventThinkingEnd)
	if !ok {
		t.Fatal("no thinking_end")
	}
	if end.Ms != 3000 {
		t.Fatalf("thinking ms = %d, want 3000", end.Ms)
	}
}

func findType(events []proto.Event, typ string) (proto.Event, bool) {
	for _, e := range events {
		if e.Type == typ {
			return e, true
		}
	}
	return proto.Event{}, false
}

func Test_LPE10_NilEmit(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hi"})
	if _, err := New(Config{Provider: p, Model: "m", Clock: clock}).Run(context.Background(), nil, message.User("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func Test_LPE11_UnknownToolContinues(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "nope", `{}`)}},
		testkit.Turn{Text: "recovered"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry()}, nil, message.User("go"))
	if res.Answer != "recovered" {
		t.Fatalf("answer = %q", res.Answer)
	}
	found := false
	for _, m := range res.Messages {
		if m.Role == message.RoleTool && strings.Contains(m.Content, "unknown tool") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unknown-tool result in transcript: %+v", res.Messages)
	}
}

func Test_LPE12_MaxTurnsExhausted(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, alwaysTool(3)...)
	res, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, MaxTurns: 3, Tools: registry(echoTool())}, nil, message.User("go"))
	if !res.Exhausted || res.Reason != "exhausted" {
		t.Fatalf("result = %+v, want exhausted", res)
	}
	if res.Turns != 3 {
		t.Fatalf("turns = %d, want 3", res.Turns)
	}
	assertRunBoundaries(t, events)
}

func Test_LPE13_MaxTurnsDefault(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	for _, maxTurns := range []int{0, -1} {
		p := testkit.NewProvider(clock, alwaysTool(defaultMaxTurns)...)
		res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, MaxTurns: maxTurns, Tools: registry(echoTool())}, nil, message.User("go"))
		if res.Turns != defaultMaxTurns || !res.Exhausted {
			t.Fatalf("MaxTurns %d: turns = %d exhausted = %v, want %d true", maxTurns, res.Turns, res.Exhausted, defaultMaxTurns)
		}
	}
}

func Test_LPE14_MaxTurnsExactlyEnough(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}},
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c2", "echo", `{}`)}},
		testkit.Turn{Text: "final"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, MaxTurns: 3, Tools: registry(echoTool())}, nil, message.User("go"))
	if res.Exhausted || res.Reason != "done" || res.Turns != 3 {
		t.Fatalf("result = %+v, want done at 3", res)
	}
}

func Test_LPE15_MissingProviderOrModel(t *testing.T) {
	c := &collector{}
	if _, err := New(Config{Model: "m", Emit: c.emit}).Run(context.Background(), nil, message.User("go")); err == nil {
		t.Fatal("expected error for missing provider")
	}
	clock := testkit.NewFakeClock(baseTime)
	if _, err := New(Config{Provider: testkit.NewProvider(clock), Emit: c.emit}).Run(context.Background(), nil, message.User("go")); err == nil {
		t.Fatal("expected error for missing model")
	}
	if len(c.all()) != 0 {
		t.Fatalf("emitted %v, want nothing", c.types())
	}
}

func Test_LPE16_RunWhileRunning(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	started := make(chan struct{}, 1)
	p := testkit.NewProvider(clock, testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "hang", `{}`)}})
	a := New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(hangingTool(started))})
	done := make(chan error, 1)
	go func() {
		_, err := a.Run(context.Background(), nil, message.User("go"))
		done <- err
	}()
	<-started
	if _, err := a.Run(context.Background(), nil, message.User("again")); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Run err = %v, want ErrBusy", err)
	}
	a.Abort()
	<-done
}

func Test_LPE17_PromptThroughOnMessage(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hi"})
	var seen []message.Message
	cfg := Config{Provider: p, Model: "m", Clock: clock, OnMessage: func(m message.Message, _ *provider.Usage) error {
		seen = append(seen, m)
		return nil
	}}
	if _, err := New(cfg).Run(context.Background(), nil, message.User("first")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) == 0 || seen[0].Role != message.RoleUser || seen[0].Content != "first" {
		t.Fatalf("first OnMessage = %+v, want user first", seen)
	}
}

func Test_LPE18_CompatRun(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "ok"})
	res, err := Run(context.Background(), Config{Provider: p, Model: "m", Clock: clock}, []message.Message{message.User("go")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "ok" || res.Turns != 1 {
		t.Fatalf("result = %+v", res)
	}
	reqs := p.Requests()
	if len(reqs) != 1 || len(reqs[0].Messages) != 1 || reqs[0].Messages[0].Content != "go" {
		t.Fatalf("provider saw %+v, want exactly the history", reqs)
	}
}

func Test_LPE19_ResultAnswer(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "the answer"})
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock}, nil, message.User("go"))
	if res.Answer != "the answer" {
		t.Fatalf("answer = %q", res.Answer)
	}
}

func Test_LPE20_NewExcludesHistory(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hi"})
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock}, []message.Message{message.User("old")}, message.User("new"))
	for _, m := range res.New {
		if m.Content == "old" {
			t.Fatalf("New contains history: %+v", res.New)
		}
	}
	found := false
	for _, m := range res.New {
		if m.Content == "new" {
			found = true
		}
	}
	if !found {
		t.Fatalf("New missing prompt: %+v", res.New)
	}
}

// panicProvider panics inside Stream, as a buggy or hostile provider might.
type panicProvider struct{}

func (panicProvider) Name() string { return "panic" }

func (panicProvider) Stream(context.Context, provider.Request, func(provider.StreamEvent)) (*provider.Response, error) {
	panic("provider boom")
}

// A panic in code the loop calls directly (here the provider) must become an
// error run with exactly one run_end, not a crashed process: Start runs the loop
// on a goroutine nobody else can recover.
func Test_LPE21_ProviderPanicEndsTheRun(t *testing.T) {
	c := &collector{}
	a := New(Config{Provider: panicProvider{}, Model: "m", Emit: c.emit})
	var res *Result
	var err error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("panic escaped the run: %v", rec)
			}
		}()
		res, err = a.Run(context.Background(), nil, message.User("go"))
	}()
	if err == nil || res == nil || res.Reason != "error" {
		t.Fatalf("res = %+v err = %v, want an error run", res, err)
	}
	ends := 0
	for _, ty := range eventTypes(c.all()) {
		if ty == proto.EventRunEnd {
			ends++
		}
	}
	if ends != 1 {
		t.Fatalf("run_end count = %d, want 1", ends)
	}
	if a.Running() {
		t.Fatal("agent still registered as running")
	}
}
