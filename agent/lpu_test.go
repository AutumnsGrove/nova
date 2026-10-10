package agent

import (
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

func Test_LPU01_UsageSummed(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}, Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}},
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c2", "echo", `{}`)}, Usage: &provider.Usage{PromptTokens: 2, CompletionTokens: 2, TotalTokens: 4}},
		testkit.Turn{Text: "done", Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 3, TotalTokens: 6}},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Usage.PromptTokens != 6 || res.Usage.CompletionTokens != 6 || res.Usage.TotalTokens != 12 {
		t.Fatalf("usage = %+v, want 6/6/12", res.Usage)
	}
}

func Test_LPU02_UsageKeptOnLaterFailure(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}, Usage: &provider.Usage{PromptTokens: 9, CompletionTokens: 4, TotalTokens: 13}},
		testkit.Turn{Err: nonRetryable()},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	res, _, err := runCollectErr(t, cfg, nil, message.User("go"))
	if err == nil {
		t.Fatal("expected error")
	}
	if res.Usage.PromptTokens != 9 || res.Usage.CompletionTokens != 4 {
		t.Fatalf("usage = %+v, want 9/4", res.Usage)
	}
}

func Test_LPU03_NoUsageReported(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock}, nil, message.User("go"))
	if res.Usage != (provider.Usage{}) {
		t.Fatalf("usage = %+v, want zero", res.Usage)
	}
}

func Test_LPU04_UsageEventsCarryTotals(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}, Usage: &provider.Usage{PromptTokens: 2, CompletionTokens: 1}},
		testkit.Turn{Text: "two", Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 2}},
	)
	_, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	var usageEvents []proto.Event
	for _, e := range events {
		if e.Type == proto.EventMessageEnd && e.Usage == nil {
			t.Fatal("message_end has no usage")
		}
		if e.Type == proto.EventUsage {
			usageEvents = append(usageEvents, e)
		}
	}
	if len(usageEvents) != 2 {
		t.Fatalf("usage events = %d, want 2", len(usageEvents))
	}
	last := usageEvents[1]
	if last.Usage == nil || last.Total == nil {
		t.Fatalf("usage event missing pointers: %+v", last)
	}
	if last.Usage.PromptTokens != 3 || last.Total.PromptTokens != 5 {
		t.Fatalf("last usage = %+v total = %+v, want 3 and 5", last.Usage, last.Total)
	}
}

func Test_LPU05_ReasoningTokensSummed(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}, Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1, ReasoningTokens: 4}},
		testkit.Turn{Text: "two", Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1, ReasoningTokens: 6}},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	if res.Usage.ReasoningTokens != 10 {
		t.Fatalf("reasoning tokens = %d, want 10", res.Usage.ReasoningTokens)
	}
}

func Test_LPU06_RunEndTotal(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}, Usage: &provider.Usage{PromptTokens: 2, CompletionTokens: 1}},
		testkit.Turn{Text: "two", Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 2}},
	)
	res, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	end, ok := findType(events, proto.EventRunEnd)
	if !ok || end.Usage == nil {
		t.Fatalf("no run_end usage: %+v", end)
	}
	if end.Usage.PromptTokens != res.Usage.PromptTokens || end.Usage.CompletionTokens != res.Usage.CompletionTokens {
		t.Fatalf("run_end usage = %+v, result usage = %+v", end.Usage, res.Usage)
	}
}
