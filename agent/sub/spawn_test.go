package sub

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

func fakeClock() *testkit.FakeClock {
	return testkit.NewFakeClock(time.Unix(0, 0))
}

// registry builds a registry with the named tools, each returning the tool name.
func registry(names ...string) *tool.Registry {
	reg := tool.NewRegistry()
	for _, name := range names {
		reg.MustRegister(tool.New(tool.Info{Name: name}, func(_ context.Context, _ json.RawMessage, _ *tool.Context) (tool.Result, error) {
			return tool.Text("ran %s", name), nil
		}))
	}
	return reg
}

// parent builds a Parent around the given provider and registry.
func parent(p provider.Provider, reg *tool.Registry) Parent {
	return Parent{Provider: p, Model: "test-model", Tools: reg, Clock: fakeClock()}
}

// TestSub_ScopedTools keeps the child to its view, and a call outside it is the
// ordinary unknown-tool error result rather than a failure.
func TestSub_LPY01_ScopedTools(t *testing.T) {
	prov := testkit.NewProvider(fakeClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "beta", Args: "{}"}}},
		testkit.Turn{Text: "answered"},
	)
	s := New(parent(prov, registry("alpha", "beta")), Options{})

	res, err := s.Spawn(context.Background(), Task{Prompt: "t", Tools: []string{"alpha"}, MaxTurns: 3})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Only alpha was offered to the model.
	defs := prov.Requests()[0].Tools
	if len(defs) != 1 || defs[0].Function.Name != "alpha" {
		t.Fatalf("offered tools = %v, want [alpha]", defs)
	}
	// The call to beta came back as an unknown-tool result, and the run continued.
	if res.Answer != "answered" {
		t.Fatalf("Answer = %q, want answered", res.Answer)
	}
}

// TestSub_EventsTagged proves the two halves of the event contract: the child's
// work reaches the parent's stream tagged with a child run id, and the events
// that address a run never do.
func TestSub_LPY02_EventsTagged(t *testing.T) {
	prov := testkit.NewProvider(fakeClock(),
		testkit.Turn{
			TextChunks: []string{"hel", "lo"},
			ToolCalls:  []testkit.Call{{ID: "1", Name: "alpha", Args: "{}"}},
			Usage:      &provider.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
		},
		testkit.Turn{Text: "done"},
	)
	var host, observed []proto.Event
	var mu sync.Mutex
	p := parent(prov, registry("alpha"))
	p.Emit = func(ev proto.Event) {
		mu.Lock()
		defer mu.Unlock()
		host = append(host, ev)
	}
	s := New(p, Options{})

	_, err := s.Spawn(context.Background(), Task{
		Prompt: "t", Tools: []string{"alpha"}, MaxTurns: 3,
		OnEvent: func(ev proto.Event) {
			mu.Lock()
			defer mu.Unlock()
			observed = append(observed, ev)
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// The caller's observer sees the whole child run, lifecycle included.
	types := eventTypes(observed)
	if !strings.Contains(types, proto.EventRunEnd) || !strings.Contains(types, proto.EventRunStart) {
		t.Fatalf("observer events = %v, want the child run lifecycle", types)
	}

	// The parent's stream sees the work, never the lifecycle.
	var sawText, sawCall bool
	ids := map[string]bool{}
	for _, ev := range host {
		switch ev.Type {
		case proto.EventRunStart, proto.EventRunEnd, proto.EventTurnStart, proto.EventTurnEnd,
			proto.EventMessageStart, proto.EventMessageEnd, proto.EventUsage, proto.EventError:
			t.Fatalf("child %s reached the parent's stream: %v", ev.Type, eventTypes(host))
		case proto.EventTextDelta:
			sawText = true
		case proto.EventToolCall:
			sawCall = true
		}
		if ev.Run == "" {
			t.Fatalf("host event %s carries no run id", ev.Type)
		}
		ids[ev.Run] = true
	}
	if !sawText || !sawCall {
		t.Fatalf("parent's stream = %v, want the child's text and tool call", eventTypes(host))
	}
	// The tag is the child's, so it cannot be mistaken for the parent's own run.
	if len(ids) != 1 {
		t.Fatalf("child events carried run ids %v, want exactly one child id", ids)
	}
	for id := range ids {
		if id == "" || !strings.HasPrefix(id, "c") {
			t.Fatalf("child run id = %q, want a child id", id)
		}
	}
}

// TestSub_HostEventFilter is the bundled extension's narrower policy: it passes
// only events that stand on their own, so a child's streaming text never
// interleaves with the parent's conversation (SUB25).
func TestSub_LPY03_HostEventFilter(t *testing.T) {
	prov := testkit.NewProvider(fakeClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "alpha", Args: "{}"}}},
		testkit.Turn{Text: "done"},
	)
	var host []string
	p := parent(prov, registry("alpha"))
	p.Policy = &permission.Policy{Rules: []permission.Rule{{Tool: "alpha", Action: permission.Deny}}}
	p.Emit = func(ev proto.Event) { host = append(host, ev.Type) }
	s := New(p, Options{HostEvents: func(typ string) bool {
		return typ == proto.EventPermission || typ == proto.EventExtensionError
	}})

	if _, err := s.Spawn(context.Background(), Task{Prompt: "t", Tools: []string{"alpha"}, MaxTurns: 3}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(host) == 0 || !strings.Contains(strings.Join(host, ","), proto.EventPermission) {
		t.Fatalf("host events = %v, want the permission decision", host)
	}
	for _, typ := range host {
		if typ != proto.EventPermission && typ != proto.EventExtensionError {
			t.Fatalf("child %s reached a narrowed host stream: %v", typ, host)
		}
	}
}

// TestSub_UsageFoldedOnce counts what the primitive reports back, which is what
// keeps a parent's cost honest under concurrent children.
func TestSub_LPY04_UsageFoldedOnce(t *testing.T) {
	spent := provider.Usage{PromptTokens: 11, CompletionTokens: 4, TotalTokens: 15, CostUSD: 0.002}
	prov := testkit.NewProvider(fakeClock(), testkit.Turn{Text: "ok", Usage: &spent})
	var calls int
	var total provider.Usage
	p := parent(prov, registry("alpha"))
	p.AddUsage = func(u provider.Usage) { calls++; total.Add(u) }
	s := New(p, Options{})

	res, err := s.Spawn(context.Background(), Task{Prompt: "t", MaxTurns: 2})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if calls != 1 {
		t.Fatalf("AddUsage called %d times, want exactly 1", calls)
	}
	if total != spent {
		t.Fatalf("folded usage = %+v, want %+v", total, spent)
	}
	if res.Usage != spent {
		t.Fatalf("Result.Usage = %+v, want %+v", res.Usage, spent)
	}
}

// TestSub_FailedChildFoldsUsage covers the failure path: a child that spent
// tokens before its provider fell over is still charged to the parent.
func TestSub_LPY05_FailedChildFoldsUsage(t *testing.T) {
	spent := provider.Usage{PromptTokens: 40, CompletionTokens: 2, TotalTokens: 42}
	prov := testkit.NewProvider(fakeClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "alpha", Args: "{}"}}, Usage: &spent},
		testkit.Turn{Err: errors.New("provider fell over")},
	)
	var calls int
	var total provider.Usage
	p := parent(prov, registry("alpha"))
	p.AddUsage = func(u provider.Usage) { calls++; total.Add(u) }
	s := New(p, Options{})

	if _, err := s.Spawn(context.Background(), Task{Prompt: "t", Tools: []string{"alpha"}, MaxTurns: 3}); err == nil {
		t.Fatal("Spawn returned nil error, want the provider failure")
	}
	if calls != 1 {
		t.Fatalf("AddUsage called %d times, want 1 even though the child failed", calls)
	}
	if total != spent {
		t.Fatalf("folded usage = %+v, want %+v", total, spent)
	}
}

// TestSub_UsageRollsIntoParentRun is the issue's end-to-end case: a child's cost
// reaches the parent's run total through tool.Context.AddUsage, and the parent's
// run still ends exactly once.
func TestSub_LPY06_UsageRollsIntoParentRun(t *testing.T) {
	childSpent := provider.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CostUSD: 0.003}
	parentSpent := provider.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, CostUSD: 0.01}

	// The child provider answers immediately with a distinct usage.
	child := testkit.NewProvider(fakeClock(), testkit.Turn{Text: "child answer", Usage: &childSpent})

	// The parent provider calls a tool that spawns, then answers.
	spawned := false
	reg := tool.NewRegistry()
	reg.MustRegister(tool.New(tool.Info{Name: "spawn_one"}, func(ctx context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		spawned = true
		s := New(Parent{
			Provider: child, Model: "test-model", Clock: fakeClock(),
			AddUsage: tctx.AddUsage,
		}, Options{})
		res, err := s.Spawn(ctx, Task{Prompt: "sub task", MaxTurns: 2})
		if err != nil {
			return tool.Errorf("spawn: %v", err), nil
		}
		return tool.Text("%s", res.Answer), nil
	}))
	parentProv := testkit.NewProvider(fakeClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "spawn_one", Args: "{}"}}, Usage: &parentSpent},
		testkit.Turn{Text: "parent answer"},
	)
	var runEnds int
	pa := agent.New(agent.Config{
		Provider: parentProv, Model: "test-model", Tools: reg, Clock: fakeClock(),
		Emit: func(ev proto.Event) {
			if ev.Type == proto.EventRunEnd {
				runEnds++
			}
		},
	})
	res, err := pa.Run(context.Background(), nil, message.User("go"))
	if err != nil {
		t.Fatalf("parent run: %v", err)
	}
	if !spawned {
		t.Fatal("the parent never called the spawn tool")
	}
	// The parent's total is its own turn plus the child's, counted once.
	if got, want := res.Usage.TotalTokens, parentSpent.TotalTokens+childSpent.TotalTokens; got != want {
		t.Fatalf("parent TotalTokens = %d, want %d (its own plus the child's once)", got, want)
	}
	if got, want := res.Usage.CostUSD, parentSpent.CostUSD+childSpent.CostUSD; got != want {
		t.Fatalf("parent CostUSD = %v, want %v", got, want)
	}
	if runEnds != 1 {
		t.Fatalf("parent run_end events = %d, want exactly 1", runEnds)
	}
}

// TestSub_DepthCap refuses a child past the limit with an error, never a panic.
func TestSub_LPY07_DepthCap(t *testing.T) {
	prov := testkit.NewProvider(fakeClock(), testkit.Turn{Text: "ok"})
	s := New(parent(prov, registry("alpha")), Options{MaxDepth: 2})

	// A parent at depth 1 may spawn a child at 2; a parent at depth 2 may not.
	if _, err := s.Spawn(context.Background(), Task{Prompt: "t", Depth: 1}); err != nil {
		t.Fatalf("depth 1 child: %v", err)
	}
	_, err := s.Spawn(context.Background(), Task{Prompt: "t", Depth: 2})
	if !errors.Is(err, ErrDepth) {
		t.Fatalf("depth 2 child: err = %v, want ErrDepth", err)
	}
}

// stepper answers a tool call on one turn and text on the next, without a
// script, so concurrent children can share it without consuming each other's
// turns.
type stepper struct{}

func (stepper) Name() string { return "stepper" }

func (stepper) Stream(_ context.Context, req provider.Request, _ func(provider.StreamEvent)) (*provider.Response, error) {
	if last := req.Messages[len(req.Messages)-1]; last.Role == message.RoleTool {
		return &provider.Response{
			Message:      message.Message{Role: message.RoleAssistant, Content: "done"},
			FinishReason: provider.NormalizeFinish("stop"),
		}, nil
	}
	call := message.ToolCall{ID: "1", Type: "function", Function: message.FunctionCall{Name: "hold", Arguments: "{}"}}
	return &provider.Response{
		Message:      message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{call}},
		FinishReason: provider.NormalizeFinish("tool_calls"),
	}, nil
}

// TestSub_ConcurrencyCap fills every slot and proves the next child waits: the
// tool blocks until the test releases it, so the count is exact rather than a
// guess about scheduling, and no real time is slept.
func TestSub_LPY08_ConcurrencyCap(t *testing.T) {
	const cap, total = 2, 5
	entered := make(chan struct{}, total)
	release := make(chan struct{})
	var inFlight, maxInFlight atomic.Int32

	hold := tool.New(tool.Info{Name: "hold"}, func(ctx context.Context, _ json.RawMessage, _ *tool.Context) (tool.Result, error) {
		n := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if n <= old || maxInFlight.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		inFlight.Add(-1)
		return tool.Text("held"), nil
	})
	reg := tool.NewRegistry()
	reg.MustRegister(hold)

	// One spawner enforces the cap, so every child shares it; the provider is
	// stateless so concurrency cannot cross the children's turns.
	s := New(Parent{Provider: stepper{}, Model: "m", Tools: reg}, Options{MaxConcurrent: cap})

	// Every child waits on one barrier, so all of them call Spawn together and a
	// missing cap shows up as an admitted child rather than a scheduling
	// accident.
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(total)
	var wg sync.WaitGroup
	errs := make([]error, total)
	answers := make([]string, total)
	for i := range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			res, err := s.Spawn(context.Background(), Task{Prompt: "t", MaxTurns: 4})
			errs[i] = err
			if res != nil {
				answers[i] = res.Answer
			}
		}()
	}
	ready.Wait()
	close(start)

	// Exactly cap children reach the tool while none can finish. The yields give
	// any child the semaphore failed to hold back the chance to be seen, so a
	// missing cap fails here instead of hiding until the release.
	for range cap {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("children never entered the tool")
		}
	}
	for range 200 {
		runtime.Gosched()
	}
	if got := inFlight.Load(); got != cap {
		t.Fatalf("in flight = %d, want exactly %d: the cap is not holding children back", got, cap)
	}
	close(release)
	wg.Wait()

	for i := range total {
		if errs[i] != nil {
			t.Fatalf("child %d: %v", i, errs[i])
		}
		// Completion order does not change a child's answer.
		if answers[i] != "done" {
			t.Fatalf("child %d answer = %q, want done", i, answers[i])
		}
	}
	if got := maxInFlight.Load(); got != cap {
		t.Fatalf("max in flight = %d, want exactly %d", got, cap)
	}
}

// TestSub_CancelStopsChildren cancels the parent and expects every child to stop
// without leaking a goroutine.
func TestSub_LPY09_CancelStopsChildren(t *testing.T) {
	testkit.NoLeaks(t)
	entered := make(chan struct{}, 1)
	toolDone := make(chan struct{})
	block := tool.New(tool.Info{Name: "block"}, func(ctx context.Context, _ json.RawMessage, _ *tool.Context) (tool.Result, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		close(toolDone)
		return tool.Text("cancelled"), nil
	})
	reg := tool.NewRegistry()
	reg.MustRegister(block)
	prov := testkit.NewProvider(fakeClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "block", Args: "{}"}}},
	)
	s := New(Parent{Provider: prov, Model: "m", Tools: reg, Clock: fakeClock()}, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := s.Spawn(ctx, Task{Prompt: "t"})
		errCh <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Spawn returned nil, want the cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Spawn did not return after the parent was cancelled")
	}
	<-toolDone
}

// TestSub_PanicInToolIsAnErrorResult proves a panicking child tool becomes an
// error result rather than taking the parent down.
func TestSub_LPY10_PanicInToolIsAnErrorResult(t *testing.T) {
	reg := tool.NewRegistry()
	reg.MustRegister(tool.New(tool.Info{Name: "boom"}, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		panic("kaboom")
	}))
	prov := testkit.NewProvider(fakeClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "boom", Args: "{}"}}},
		testkit.Turn{Text: "recovered"},
	)
	s := New(parent(prov, reg), Options{})
	res, err := s.Spawn(context.Background(), Task{Prompt: "t", Tools: []string{"boom"}, MaxTurns: 3})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.Answer != "recovered" {
		t.Fatalf("Answer = %q, want the run to continue after the panic", res.Answer)
	}
}

func eventTypes(events []proto.Event) string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return strings.Join(out, ",")
}
