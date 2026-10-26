package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
	"github.com/AutumnsGrove/nova/usage"
)

func bashRegistry() *tool.Registry {
	reg := tool.NewRegistry()
	reg.MustRegister(tool.New(tool.Info{Name: "bash"}, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Text("ran"), nil
	}))
	return reg
}

func fakeClock() testkit.Clock { return testkit.NewFakeClock(time.Unix(0, 0)) }

// The host's event stream is shared with the parent run, and its subscribers
// treat run_end as the end of the whole request, so a child's run lifecycle
// must stay out of it. A permission decision is still worth showing.
func TestSubagent_SUB25_ChildLifecycleStaysOutOfHostStream(t *testing.T) {
	prov := testkit.NewProvider(fakeClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "bash", Args: "{}"}}},
		testkit.Turn{Text: "final"},
	)
	var host, observed []string
	sp := &Spawner{
		Provider: prov, Model: "m", Registry: bashRegistry(), Clock: fakeClock(),
		Policy: &permission.Policy{Rules: []permission.Rule{{Tool: "bash", Action: permission.Deny}}},
		Emit:   func(ev proto.Event) { host = append(host, ev.Type) },
	}
	req := ext.SpawnRequest{Task: "t", Tools: []string{"bash"}, MaxTurns: 3, OnEvent: func(ev proto.Event) { observed = append(observed, ev.Type) }}
	if _, err := sp.Spawn(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(observed, ","), proto.EventRunEnd) {
		t.Fatalf("the caller's observer should see every child event: %v", observed)
	}
	if !strings.Contains(strings.Join(host, ","), proto.EventPermission) {
		t.Fatalf("host events = %v, want the permission decision", host)
	}
	for _, typ := range host {
		switch typ {
		case proto.EventRunStart, proto.EventRunEnd, proto.EventTurnStart, proto.EventTurnEnd, proto.EventMessageEnd, proto.EventUsage:
			t.Fatalf("child %s reached the host stream: %v", typ, host)
		}
	}
}

func TestSubagent_SUB26_ProgressNamesEachToolOnce(t *testing.T) {
	fake := &fakeSpawn{fn: func(_ context.Context, r ext.SpawnRequest) (*ext.SpawnResult, error) {
		// The agent loop reports a call by name in tool_call and by id alone in
		// tool_start.
		for _, name := range []string{"read", "grep"} {
			r.OnEvent(proto.Event{Type: proto.EventToolCall, Call: &proto.ToolCall{ID: name, Name: name}})
			r.OnEvent(proto.Event{Type: proto.EventToolStart, CallID: name})
		}
		return &ext.SpawnResult{Answer: "ok", Turns: 2, Reason: "done"}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	p := &progress{}
	_, _ = e.run(context.Background(), json.RawMessage(`{"task":"t"}`), &tool.Context{Emit: p.emit})
	if got := strings.Join(p.all(), "|"); got != "▸ read|▸ grep|done after 2 turn(s)" {
		t.Fatalf("progress = %q", got)
	}
}

func TestSubagent_SUB27_AbortedChildStillCountsItsUsage(t *testing.T) {
	spent := provider.Usage{PromptTokens: 100}
	prov := testkit.NewProvider(fakeClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "bash", Args: "{}"}}, Usage: &spent},
		testkit.Turn{Err: errors.New("provider fell over")},
	)
	ledger := usage.NewLedger(nil)
	sp := &Spawner{Provider: prov, Model: "m", Registry: bashRegistry(), Ledger: ledger, Clock: fakeClock()}
	res, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t", Tools: []string{"bash"}, MaxTurns: 3})
	if err == nil {
		t.Fatal("want the provider error")
	}
	if got := ledger.Total().PromptTokens; got != 100 {
		t.Fatalf("ledger prompt tokens = %d, want the 100 the child spent", got)
	}
	if res == nil || res.Usage.PromptTokens != 100 {
		t.Fatalf("a failed run should still report its usage: %+v", res)
	}

	// The tool reports that usage to the run, so the footer and run_end include it.
	fake := &fakeSpawn{fn: func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error) {
		return res, err
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	var added provider.Usage
	out, _ := e.run(context.Background(), json.RawMessage(`{"task":"t"}`), &tool.Context{AddUsage: func(u provider.Usage) { added.Add(u) }})
	if !out.IsError || added.PromptTokens != 100 {
		t.Fatalf("result = %+v, reported usage = %+v", out, added)
	}
}

func TestSubagent_SUB28_NestedCallDoesNotWaitOnItsParentsSlot(t *testing.T) {
	cfg := &config.Config{Subagent: config.SubagentConfig{MaxDepth: 2, MaxConcurrent: 1}}
	var e *Extension
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var inner tool.Result
	fake := &fakeSpawn{fn: func(_ context.Context, r ext.SpawnRequest) (*ext.SpawnResult, error) {
		if r.Depth == 0 {
			// The child calls subagent while its parent still holds the only slot.
			inner, _ = e.run(ctx, json.RawMessage(`{"task":"nested"}`), &tool.Context{Data: Depth{N: 1}})
		}
		return &ext.SpawnResult{Answer: "done", Reason: "done"}, nil
	}}
	e = setup(defaultRegistry(), cfg, fake.spawn)
	if res, _ := e.run(ctx, json.RawMessage(`{"task":"outer"}`), nil); res.IsError {
		t.Fatalf("outer = %q", res.Content)
	}
	if inner.IsError || inner.Content != "done" {
		t.Fatalf("nested call = %+v, want it to run", inner)
	}
}

func TestSubagent_SUB29_AnswerCutOnCharacterBoundary(t *testing.T) {
	answer := "a" + strings.Repeat("é", resultCap) // the cap lands between the two bytes of a character
	fake := &fakeSpawn{fn: func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error) {
		return &ext.SpawnResult{Answer: answer, Reason: "done"}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	res, _ := e.run(context.Background(), json.RawMessage(`{"task":"t"}`), nil)
	if !res.Truncated || !utf8.ValidString(res.Content) || len(res.Content) > resultCap {
		t.Fatalf("truncated=%v valid=%v len=%d", res.Truncated, utf8.ValidString(res.Content), len(res.Content))
	}
}

func TestSubagent_SUB30_SpawnerCapsDepth(t *testing.T) {
	sp := &Spawner{Provider: scriptedAnswer(provider.Usage{}), Model: "m", Registry: tool.NewRegistry(), MaxDepth: 10, Clock: fakeClock()}
	if _, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t", Depth: maxDepthCap}); err == nil {
		t.Fatalf("a configured depth above %d should still stop at %d", maxDepthCap, maxDepthCap)
	}
}

// TestSubagent_SUB34_ChildDataCarriesPrimitiveDepth pins the seam between the
// primitive and the extension: a spawned child's tool data is the primitive's
// Depth, so a nested subagent tool reads the depth it is really at. Before the
// two shared one type, the extension's reader saw a value it did not recognise
// and believed every nested child was top level, which silently disabled the
// depth cap.
func TestSubagent_SUB34_ChildDataCarriesPrimitiveDepth(t *testing.T) {
	var childData any
	reg := tool.NewRegistry()
	reg.MustRegister(tool.New(tool.Info{Name: "peek"}, func(_ context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		childData = tctx.Data
		return tool.Text("ok"), nil
	}))
	prov := testkit.NewProvider(fakeClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "peek", Args: "{}"}}},
		testkit.Turn{Text: "done"},
	)
	sp := &Spawner{Provider: prov, Model: "m", Registry: reg, Clock: fakeClock(), MaxDepth: 3}
	if _, err := sp.Spawn(context.Background(), ext.SpawnRequest{
		Task: "t", Tools: []string{"peek"}, MaxTurns: 3, Depth: 1,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := depthOf(&tool.Context{Data: childData}); got != 2 {
		t.Fatalf("child depth read back as %d, want 2 (parent at 1 plus one)", got)
	}
}
