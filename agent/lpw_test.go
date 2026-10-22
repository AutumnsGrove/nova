package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// eventsJSON serializes an event stream for a golden comparison.
func eventsJSON(t *testing.T, events []proto.Event) string {
	t.Helper()
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	return string(data) + "\n"
}

func Test_LPW01_WrapUpAnswersAfterTheBudget(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		namedToolTurn("echo"),
		namedToolTurn("echo"),
		testkit.Turn{Text: "the wrapped answer"},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()), MaxTurns: 2, WrapUp: &WrapUp{}}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	assertRunBoundaries(t, events)
	if res.Reason != "done" || res.Exhausted {
		t.Fatalf("reason=%q exhausted=%v, want a normal done", res.Reason, res.Exhausted)
	}
	if res.Answer != "the wrapped answer" || res.Turns != 3 {
		t.Fatalf("answer=%q turns=%d", res.Answer, res.Turns)
	}
	reqs := p.Requests()
	if len(reqs) != 3 {
		t.Fatalf("provider requests = %d, want 3", len(reqs))
	}
	if len(reqs[2].Tools) != 0 {
		t.Errorf("wrap-up offered %d tools, want none", len(reqs[2].Tools))
	}
	last := lastMessage(t, reqs, 2)
	if last.Role != message.RoleUser || last.Content != DefaultWrapUpInstruction || !last.Synthetic {
		t.Errorf("wrap-up message = %+v, want the synthetic default instruction", last)
	}
	if got := countType(events, proto.EventTurnStart); got != 3 {
		t.Errorf("turn_start count = %d, want 3", got)
	}
}

func Test_LPW02_WrapUpToolCallFallsBack(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var ran atomic.Int32
	counter := toolOf("count", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		ran.Add(1)
		return tool.Text("counted"), nil
	})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "count", `{}`)}, Usage: &provider.Usage{PromptTokens: 1}},
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c2", "count", `{}`)}, Usage: &provider.Usage{PromptTokens: 2}},
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c3", "count", `{}`)}, Usage: &provider.Usage{PromptTokens: 4}},
	)
	cfg := Config{
		Provider: p, Model: "m", Clock: clock, Tools: registry(counter), MaxTurns: 2,
		WrapUp: &WrapUp{Fallback: "could not finish"},
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	assertRunBoundaries(t, events)
	if res.Reason != "exhausted" || !res.Exhausted {
		t.Fatalf("reason=%q exhausted=%v, want exhausted", res.Reason, res.Exhausted)
	}
	if res.Answer != "could not finish" || res.Turns != 3 {
		t.Fatalf("answer=%q turns=%d", res.Answer, res.Turns)
	}
	if got := ran.Load(); got != 2 {
		t.Errorf("tool ran %d times, want 2 (the wrap-up call must not execute)", got)
	}
	if got := countType(events, proto.EventToolStart); got != 2 {
		t.Errorf("tool_start count = %d, want 2", got)
	}
	if res.Usage.PromptTokens != 7 {
		t.Errorf("usage = %+v, want the wrap-up call counted (7)", res.Usage)
	}
	last := res.Messages[len(res.Messages)-1]
	if last.Role != message.RoleAssistant || last.Content != "could not finish" || !last.Synthetic || len(last.ToolCalls) != 0 {
		t.Errorf("last message = %+v, want the synthetic fallback", last)
	}
}

func Test_LPW03_WrapUpUnsetIsUnchanged(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, namedToolTurn("echo"), namedToolTurn("echo"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()), MaxTurns: 2}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	assertRunBoundaries(t, events)
	if !res.Exhausted || res.Reason != "exhausted" {
		t.Fatalf("reason=%q exhausted=%v, want exhausted", res.Reason, res.Exhausted)
	}
	if len(p.Requests()) != 2 {
		t.Fatalf("provider requests = %d, want 2 (no wrap-up)", len(p.Requests()))
	}
	if got := countType(events, proto.EventTurnStart); got != 2 {
		t.Errorf("turn_start count = %d, want 2", got)
	}
	testkit.Golden(t, "wrapup_unset", eventsJSON(t, events))
}

func Test_LPW04_WrapUpProviderErrorKeepsUsage(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}, Usage: &provider.Usage{PromptTokens: 3}},
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c2", "echo", `{}`)}, Usage: &provider.Usage{PromptTokens: 4}},
		testkit.Turn{Err: errors.New("wrap-up provider failed")},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()), MaxTurns: 2, WrapUp: &WrapUp{}}

	res, events, err := runCollectErr(t, cfg, nil, message.User("go"))
	if err == nil || !strings.Contains(err.Error(), "wrap-up provider failed") {
		t.Fatalf("error = %v, want the provider failure", err)
	}
	if res == nil || res.Reason != "error" || res.Usage.PromptTokens != 7 {
		t.Fatalf("result = %+v, want the spend accrued so far", res)
	}
	assertRunBoundaries(t, events)
	if len(p.Requests()) != 3 {
		t.Fatalf("provider requests = %d, want 3", len(p.Requests()))
	}
}

// thirdCallProvider signals when the third Stream call starts, so a test can
// cancel exactly during the wrap-up request.
type thirdCallProvider struct {
	*testkit.Provider
	third chan struct{}
	mu    sync.Mutex
	calls int
}

func (p *thirdCallProvider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 3 {
		close(p.third)
	}
	return p.Provider.Stream(ctx, req, onEvent)
}

func Test_LPW05_WrapUpCancellationReturnsPartial(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	base := testkit.NewProvider(clock,
		namedToolTurn("echo"),
		namedToolTurn("echo"),
		testkit.Turn{Hang: true},
	)
	p := &thirdCallProvider{Provider: base, third: make(chan struct{})}
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()), MaxTurns: 2, WrapUp: &WrapUp{}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var res *Result
	var err error
	go func() {
		res, err = New(cfg).Run(ctx, nil, message.User("go"))
		close(done)
	}()
	<-p.third
	cancel()
	<-done

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if res == nil || res.Reason != "aborted" {
		t.Fatalf("result = %+v, want an aborted run", res)
	}
	last := res.Messages[len(res.Messages)-1]
	if last.Content != DefaultWrapUpInstruction || !last.Synthetic {
		t.Errorf("last message = %+v, want the wrap-up instruction", last)
	}
}

func Test_LPW06_TurnEndCannotExtendTheBudget(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		namedToolTurn("echo"),
		namedToolTurn("echo"),
		testkit.Turn{Text: "wrapped"},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()), MaxTurns: 2, WrapUp: &WrapUp{}}
	calls := 0
	cfg.Hooks.TurnEnd = func(context.Context, Turn) TurnDecision {
		calls++
		return TurnDecision{Continue: true}
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	assertRunBoundaries(t, events)
	if len(p.Requests()) != 3 {
		t.Fatalf("provider requests = %d, want 3 (the hook must not add a turn)", len(p.Requests()))
	}
	if calls != 3 {
		t.Errorf("TurnEnd calls = %d, want 2 normal turns plus the wrap-up", calls)
	}
	if res.Reason != "done" || res.Answer != "wrapped" {
		t.Fatalf("reason=%q answer=%q", res.Reason, res.Answer)
	}
}

func Test_LPW07_WrapUpInstructionIsOverridable(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		namedToolTurn("echo"),
		namedToolTurn("echo"),
		testkit.Turn{Text: "wrapped"},
	)
	cfg := Config{
		Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()), MaxTurns: 2,
		WrapUp: &WrapUp{Instruction: "finish now"},
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Answer != "wrapped" {
		t.Fatalf("answer = %q", res.Answer)
	}
	last := lastMessage(t, p.Requests(), 2)
	if last.Content != "finish now" || !last.Synthetic {
		t.Fatalf("wrap-up message = %+v, want the custom instruction", last)
	}
}
