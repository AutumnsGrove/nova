package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// echoPolicy builds a policy with a single echo rule.
func echoPolicy(action permission.Action) *permission.Policy {
	return &permission.Policy{
		Default: permission.Allow,
		Rules:   []permission.Rule{{Tool: "echo", Action: action}},
	}
}

// ranTool builds a tool that records whether it ran.
func ranTool(ran *bool) tool.Tool {
	return toolOf("echo", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		*ran = true
		return tool.Text("ran"), nil
	})
}

func Test_LPP01_NoPolicy(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(ranTool(&ran))}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if !ran || !hasToolContent(res.Messages, "ran") {
		t.Fatalf("tool did not run: %+v", res.Messages)
	}
}

func Test_LPP02_DenyRule(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	hookRan := false
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(ranTool(&ran)), Policy: echoPolicy(permission.Deny)}
	cfg.Hooks.ToolCall = func(context.Context, ToolCall) *CallDecision { hookRan = true; return nil }
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if ran {
		t.Fatal("denied tool ran")
	}
	if hookRan {
		t.Fatal("ToolCall hook ran for a denied call")
	}
	if !hasToolContent(res.Messages, "error: blocked by policy: rule 0: deny echo") {
		t.Fatalf("no policy-blocked result: %+v", res.Messages)
	}
}

func Test_LPP03_AllowRule(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(ranTool(&ran)), Policy: echoPolicy(permission.Allow)}
	runCollect(t, cfg, nil, message.User("go"))
	if !ran {
		t.Fatal("allowed tool did not run")
	}
}

func Test_LPP04_AskApproved(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	asked := 0
	cfg := Config{
		Provider: p, Model: "m", Clock: clock, Tools: registry(ranTool(&ran)),
		Policy: &permission.Policy{Default: permission.Ask},
		Ask: func(context.Context, Question) (bool, error) {
			asked++
			return true, nil
		},
	}
	runCollect(t, cfg, nil, message.User("go"))
	if !ran || asked != 1 {
		t.Fatalf("ran = %v asked = %d, want true, 1", ran, asked)
	}
}

func Test_LPP05_AskDenied(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{
		Provider: p, Model: "m", Clock: clock, Tools: registry(ranTool(&ran)),
		Policy: &permission.Policy{Default: permission.Ask},
		Ask:    func(context.Context, Question) (bool, error) { return false, nil },
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if ran {
		t.Fatal("denied tool ran")
	}
	if !hasToolContent(res.Messages, "error: not approved: default: ask") {
		t.Fatalf("no not-approved result: %+v", res.Messages)
	}
}

func Test_LPP06_AskError(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{
		Provider: p, Model: "m", Clock: clock, Tools: registry(ranTool(&ran)),
		Policy: &permission.Policy{Default: permission.Ask},
		Ask:    func(context.Context, Question) (bool, error) { return false, errors.New("no ui") },
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if ran {
		t.Fatal("tool ran after Ask error")
	}
	if !hasToolPrefix(res.Messages, "error: not approved:") {
		t.Fatalf("no not-approved result: %+v", res.Messages)
	}
	if res.Reason != "done" {
		t.Fatalf("reason = %q, want done (run continues)", res.Reason)
	}
}

func Test_LPP07_HeadlessDeny(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{
		Provider: p, Model: "m", Clock: clock, Tools: registry(ranTool(&ran)),
		Policy: &permission.Policy{Default: permission.Ask, Headless: permission.Deny},
		// Ask is nil: there is no one to ask.
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if ran {
		t.Fatal("headless-denied tool ran")
	}
	if !hasToolPrefix(res.Messages, "error: blocked by policy:") {
		t.Fatalf("no blocked result: %+v", res.Messages)
	}
}

func Test_LPP08_PermissionEvent(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(ranTool(&ran)), Policy: echoPolicy(permission.Deny)}
	_, events := runCollect(t, cfg, nil, message.User("go"))
	pe, ok := findType(events, proto.EventPermission)
	if !ok {
		t.Fatalf("no permission event: %v", eventTypes(events))
	}
	if pe.Decision != string(permission.Deny) || pe.Reason == "" {
		t.Fatalf("permission event = %+v", pe)
	}
}

func Test_LPP09_AskHonoursCancellation(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	entered := make(chan struct{})
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{
		Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()),
		Policy: &permission.Policy{Default: permission.Ask},
		Ask: func(ctx context.Context, _ Question) (bool, error) {
			close(entered)
			<-ctx.Done()
			return false, ctx.Err()
		},
	}
	a := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan runOutcome, 1)
	go func() { done <- runAsyncCtx(a, ctx) }()
	<-entered
	cancel()
	out := <-done
	if out.err == nil {
		t.Fatalf("run returned nil error, want context cancellation")
	}
	if out.res.Reason != "aborted" {
		t.Fatalf("reason = %q, want aborted", out.res.Reason)
	}
}

func runAsyncCtx(a *Agent, ctx context.Context) runOutcome {
	res, err := a.Run(ctx, nil, message.User("go"))
	return runOutcome{res, err}
}

func Test_LPP10_ConcurrentAsks(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var asked int32
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: callN("echo", 3)},
		textTurn("done"),
	)
	cfg := Config{
		Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()),
		Policy: &permission.Policy{Default: permission.Ask},
		Ask: func(context.Context, Question) (bool, error) {
			atomic.AddInt32(&asked, 1)
			return true, nil
		},
	}
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	out := <-done
	if out.err != nil {
		t.Fatalf("run: %v", out.err)
	}
	if got := atomic.LoadInt32(&asked); got != 3 {
		t.Fatalf("ask count = %d, want 3", got)
	}
}
