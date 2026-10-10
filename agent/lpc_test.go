package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// cancellingTool signals, waits for ctx, then returns ctx.Err().
func cancellingTool(started chan<- struct{}) tool.Tool {
	return toolOf("hang", true, func(ctx context.Context, _ json.RawMessage, _ *tool.Context) (tool.Result, error) {
		started <- struct{}{}
		<-ctx.Done()
		return tool.Result{}, ctx.Err()
	})
}

func TestLPC01_CancelBeforeFirstRequest(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("never"))
	c := &collector{}
	cfg := Config{Provider: p, Model: "m", Clock: clock, Emit: c.emit}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := runAsyncCtx(New(cfg), ctx)
	if !errors.Is(out.err, context.Canceled) || out.res.Reason != "aborted" {
		t.Fatalf("err = %v reason = %q", out.err, out.res.Reason)
	}
	if got := eventTypes(c.all()); len(got) != 2 || got[0] != proto.EventRunStart || got[1] != proto.EventRunEnd {
		t.Fatalf("events = %v, want [run_start run_end]", got)
	}
}

// hangingStreamProvider blocks inside Stream until the context is cancelled.
type hangingStreamProvider struct{ entered chan struct{} }

func (p *hangingStreamProvider) Name() string { return "hang" }

func (p *hangingStreamProvider) Stream(ctx context.Context, _ provider.Request, _ func(provider.StreamEvent)) (*provider.Response, error) {
	close(p.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestLPC02_CancelDuringStreaming(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	p := &hangingStreamProvider{entered: make(chan struct{})}
	c := &collector{}
	cfg := Config{Provider: p, Model: "m", Clock: clock, Emit: c.emit}
	a := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan runOutcome, 1)
	go func() { done <- runAsyncCtx(a, ctx) }()
	<-p.entered
	cancel()
	out := <-done
	if !errors.Is(out.err, context.Canceled) || out.res.Reason != "aborted" {
		t.Fatalf("err = %v reason = %q", out.err, out.res.Reason)
	}
	for _, m := range out.res.Messages {
		if m.Role == message.RoleAssistant {
			t.Fatalf("partial assistant message kept: %+v", out.res.Messages)
		}
	}
}

func TestLPC06_NoGoroutineLeak(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	p := &hangingStreamProvider{entered: make(chan struct{})}
	a := New(Config{Provider: p, Model: "m", Clock: clock})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = a.Run(ctx, nil, message.User("go"))
		close(done)
	}()
	<-p.entered
	cancel()
	<-done
}

func TestLPC03_CancelDuringTool(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	started := make(chan struct{}, 1)
	p := testkit.NewProvider(clock, testkit.Turn{ToolCalls: callN("hang", 3)})
	cfg := Config{Provider: p, Model: "m", Clock: clock, MaxParallel: 1, Tools: registry(cancellingTool(started))}
	a := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan runOutcome, 1)
	go func() { done <- runAsyncCtx(a, ctx) }()
	<-started
	cancel()
	out := <-done
	if !errors.Is(out.err, context.Canceled) || out.res.Reason != "aborted" {
		t.Fatalf("err = %v reason = %q", out.err, out.res.Reason)
	}
	if len(toolResultIDs(out.res.Messages)) != 3 {
		t.Fatalf("results = %v, want 3", toolResultIDs(out.res.Messages))
	}
	found := false
	for _, m := range out.res.Messages {
		if m.Role == message.RoleTool && m.Content == "error: cancelled" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no error: cancelled result: %+v", out.res.Messages)
	}
	assertValid(t, out.res.Messages)
}

func TestLPC04_CancelBetweenTurns(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, toolTurn(), textTurn("never"))
	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	cfg.Hooks.TurnEnd = func(context.Context, Turn) TurnDecision {
		cancel()
		return TurnDecision{}
	}
	out := runAsyncCtx(New(cfg), ctx)
	if !errors.Is(out.err, context.Canceled) || out.res.Reason != "aborted" {
		t.Fatalf("err = %v reason = %q", out.err, out.res.Reason)
	}
	assertValid(t, out.res.Messages)
}

func TestLPC05_CancelWhileParallelToolsRun(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	started := make(chan struct{}, 5)
	p := testkit.NewProvider(clock, testkit.Turn{ToolCalls: callN("hang", 5)})
	cfg := Config{Provider: p, Model: "m", Clock: clock, MaxParallel: 5, Tools: registry(cancellingTool(started))}
	a := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan runOutcome, 1)
	go func() { done <- runAsyncCtx(a, ctx) }()
	waitFor(t, started, 5)
	cancel()
	out := <-done
	if !errors.Is(out.err, context.Canceled) || out.res.Reason != "aborted" {
		t.Fatalf("err = %v reason = %q", out.err, out.res.Reason)
	}
	if got := len(toolResultIDs(out.res.Messages)); got != 5 {
		t.Fatalf("results = %d, want 5", got)
	}
	assertValid(t, out.res.Messages)
}

func TestLPC07_ToolIgnoresCancellation(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	slow := toolOf("slow", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		started <- struct{}{}
		<-release
		return tool.Text("finished"), nil
	})
	p := testkit.NewProvider(clock, testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "slow", `{}`)}})
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(slow)}
	a := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan runOutcome, 1)
	go func() { done <- runAsyncCtx(a, ctx) }()
	<-started
	cancel()
	select {
	case <-done:
		t.Fatal("run returned before the ignoring tool finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	out := <-done
	if !errors.Is(out.err, context.Canceled) || out.res.Reason != "aborted" {
		t.Fatalf("err = %v reason = %q", out.err, out.res.Reason)
	}
	if !hasToolContent(out.res.Messages, "finished") {
		t.Fatalf("tool result lost: %+v", out.res.Messages)
	}
}
