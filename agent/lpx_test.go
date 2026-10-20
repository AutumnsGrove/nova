package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// queueUpdates returns the steering and follow-up counts of every queue_update
// event, in emit order.
func queueUpdates(events []proto.Event) (steering, followUp []int) {
	for _, e := range events {
		if e.Type != proto.EventQueueUpdate {
			continue
		}
		steering = append(steering, *e.Steering)
		followUp = append(followUp, *e.FollowUp)
	}
	return steering, followUp
}

func Test_LPQ13_QueueUpdateWhenSteeringIsConsumed(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var a *Agent
	tl := toolOf("steer", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		if err := a.Steer(message.User("steered")); err != nil {
			t.Errorf("Steer: %v", err)
		}
		return tool.Text("ok"), nil
	})
	p := testkit.NewProvider(clock, namedToolTurn("steer"), textTurn("done"))
	c := &collector{}
	a = New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(tl), Emit: c.emit})
	if _, err := a.Run(context.Background(), nil, message.User("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	steering, _ := queueUpdates(c.all())
	if len(steering) != 2 || steering[0] != 1 || steering[1] != 0 {
		t.Fatalf("steering counts = %v, want [1 0]: the TUI needs the drop to un-queue the message", steering)
	}
}

func Test_LPQ14_QueueUpdateWhenFollowUpIsConsumed(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var a *Agent
	p := &steerProvider{}
	c := &collector{}
	a = New(Config{Provider: p, Model: "m", Clock: clock, Emit: c.emit})
	p.steer = func() {
		if err := a.FollowUp(message.User("later")); err != nil {
			t.Errorf("FollowUp: %v", err)
		}
	}
	if _, err := a.Run(context.Background(), nil, message.User("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, followUp := queueUpdates(c.all())
	if len(followUp) != 2 || followUp[0] != 1 || followUp[1] != 0 {
		t.Fatalf("follow-up counts = %v, want [1 0]", followUp)
	}
}

func Test_LPQ15_ConcurrentQueueUpdatesEndAtTheTrueCount(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	started := make(chan struct{}, 1)
	p := testkit.NewProvider(clock, namedToolTurn("hang"))
	c := &collector{}
	a := New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(hangingTool(started)), Emit: c.emit})
	done := make(chan runOutcome, 1)
	go func() { done <- runAsyncCtx(a, context.Background()) }()
	<-started

	const n = 50
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.Steer(message.User("s")); err != nil {
				t.Errorf("Steer: %v", err)
			}
		}()
	}
	wg.Wait()
	steering, _ := queueUpdates(c.all())
	if len(steering) != n || steering[n-1] != n {
		t.Fatalf("last queue_update reports %v, want %d: events must reflect the final queue", steering, n)
	}
	a.Abort()
	<-done
}

func Test_LPQ16_NothingIsAcceptedOnceTheRunIsEnding(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	var a *Agent
	var steerErr, followErr error
	cfg := Config{Provider: testkit.NewProvider(clock, textTurn("done")), Model: "m", Clock: clock}
	cfg.Hooks.AgentEnd = func(context.Context, *Result) {
		steerErr = a.Steer(message.User("too late"))
		followErr = a.FollowUp(message.User("too late"))
	}
	a = New(cfg)
	if _, err := a.Run(context.Background(), nil, message.User("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if steerErr != ErrNotRunning || followErr != ErrNotRunning {
		t.Fatalf("Steer = %v, FollowUp = %v; a message accepted after the last decision is silently lost", steerErr, followErr)
	}
	if s, f := a.Queued(); s != 0 || f != 0 {
		t.Fatalf("queues hold %d and %d messages after the run", s, f)
	}
}

func Test_LPT21_ProgressAfterToolReturnsIsHarmless(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	var late func(tool.Update)
	tl := toolOf("late", false, func(_ context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		late = tctx.Emit
		return tool.Text("ok"), nil
	})
	p := testkit.NewProvider(clock, namedToolTurn("late"), textTurn("done"))
	if _, err := New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(tl)}).Run(context.Background(), nil, message.User("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if late == nil {
		t.Fatal("the tool never ran")
	}
	// A tool that leaked a goroutine may report progress after it returned; that
	// must be dropped, not panic the process with a send on a closed channel.
	late(tool.Update{Text: "too late"})
}

func Test_LPT22_EmptyResultFromHookIsNormalisedOnce(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	cfg.Hooks.ToolResult = func(context.Context, ToolCall, tool.Result) tool.Result { return tool.Result{} }
	res, events := runCollect(t, cfg, nil, message.User("go"))
	var end *proto.ToolResult
	for _, e := range events {
		if e.Type == proto.EventToolEnd {
			end = e.Result
		}
	}
	if end == nil || end.Content != "(empty result)" {
		t.Fatalf("tool_end = %+v, want the recorded content (empty result)", end)
	}
	if !hasToolContent(res.Messages, "(empty result)") {
		t.Fatalf("transcript lacks the normalised result: %+v", res.Messages)
	}
}

func Test_LPT23_TruncatedArgumentsStillEncode(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{"a":`)}},
		testkit.Turn{Text: "done"},
	)
	_, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	if countType(events, proto.EventToolCall) != 1 {
		t.Fatalf("want one tool_call event, got %v", eventTypes(events))
	}
	// A model cut off mid-arguments is routine; the frame must still serialise
	// or the transports lose the whole call.
	for _, e := range events {
		if _, err := json.Marshal(e); err != nil {
			t.Fatalf("%s event does not encode: %v", e.Type, err)
		}
	}
}

func Test_LPP11_UnknownPolicyActionFailsClosed(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	// An unvalidated policy with a mistyped action must not let the call through.
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(ranTool(&ran)), Policy: &permission.Policy{Default: "alow"}}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if ran {
		t.Fatal("a call ran under a policy whose action is not allow, ask or deny")
	}
	if !hasToolContent(res.Messages, "error: blocked by policy: default: alow") {
		t.Fatalf("no blocked result: %+v", res.Messages)
	}
}
