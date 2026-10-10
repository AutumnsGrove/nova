package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

func textTurn(text string) testkit.Turn { return testkit.Turn{Text: text} }

func toolTurn() testkit.Turn {
	return testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}}
}

func TestLPH01_ToolCallHookBlocks(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ran := false
	tl := toolOf("echo", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		ran = true
		return tool.Text("ran"), nil
	})
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(tl)}
	cfg.Hooks.ToolCall = func(context.Context, ToolCall) *CallDecision {
		return &CallDecision{Block: true, Reason: "nope"}
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if ran {
		t.Fatal("blocked tool ran")
	}
	if !hasToolContent(res.Messages, "error: blocked: nope") {
		t.Fatalf("no blocked result: %+v", res.Messages)
	}
}

func hasToolContent(msgs []message.Message, want string) bool {
	for _, m := range msgs {
		if m.Role == message.RoleTool && m.Content == want {
			return true
		}
	}
	return false
}

func hasToolPrefix(msgs []message.Message, prefix string) bool {
	for _, m := range msgs {
		if m.Role == message.RoleTool && strings.HasPrefix(m.Content, prefix) {
			return true
		}
	}
	return false
}

func TestLPH02_ToolCallHookRewritesArguments(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	cfg.Hooks.ToolCall = func(context.Context, ToolCall) *CallDecision {
		return &CallDecision{Arguments: []byte(`{"text":"new"}`)}
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if !hasToolContent(res.Messages, "echo:new") {
		t.Fatalf("tool did not see new arguments: %+v", res.Messages)
	}
}

func TestLPH03_ToolCallHookNil(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	cfg.Hooks.ToolCall = func(context.Context, ToolCall) *CallDecision { return nil }
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if !hasToolContent(res.Messages, "echo:echoed") {
		t.Fatalf("tool did not run: %+v", res.Messages)
	}
}

func TestLPH04_ToolResultHookReplaces(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	cfg.Hooks.ToolResult = func(context.Context, ToolCall, tool.Result) tool.Result {
		return tool.Text("replaced")
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if !hasToolContent(res.Messages, "replaced") {
		t.Fatalf("result not replaced: %+v", res.Messages)
	}
}

func TestLPH05_HookPanics(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	boom := func() { panic("hook boom") }
	cases := []struct {
		name  string
		setup func(*Config)
		turns []testkit.Turn
	}{
		{"before_provider_request", func(c *Config) {
			c.Hooks.BeforeProviderRequest = func(context.Context, *provider.Request) { boom() }
		}, []testkit.Turn{textTurn("done")}},
		{"transform_context", func(c *Config) {
			c.Hooks.TransformContext = func(context.Context, []message.Message) []message.Message { boom(); return nil }
		}, []testkit.Turn{textTurn("done")}},
		{"before_turn", func(c *Config) {
			c.Hooks.BeforeTurn = func(context.Context, []message.Message) ([]message.Message, error) {
				boom()
				return nil, nil
			}
		}, []testkit.Turn{textTurn("done")}},
		{"message_end", func(c *Config) {
			c.Hooks.MessageEnd = func(context.Context, message.Message) message.Message { boom(); return message.Message{} }
		}, []testkit.Turn{textTurn("done")}},
		{"tool_call", func(c *Config) {
			c.Hooks.ToolCall = func(context.Context, ToolCall) *CallDecision { boom(); return nil }
		}, []testkit.Turn{toolTurn(), textTurn("done")}},
		{"tool_result", func(c *Config) {
			c.Hooks.ToolResult = func(context.Context, ToolCall, tool.Result) tool.Result { boom(); return tool.Result{} }
		}, []testkit.Turn{toolTurn(), textTurn("done")}},
		{"turn_end", func(c *Config) {
			c.Hooks.TurnEnd = func(context.Context, Turn) TurnDecision { boom(); return TurnDecision{} }
		}, []testkit.Turn{textTurn("done")}},
		{"agent_end", func(c *Config) {
			c.Hooks.AgentEnd = func(context.Context, *Result) { boom() }
		}, []testkit.Turn{textTurn("done")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testkit.NewProvider(clock, tc.turns...)
			cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
			tc.setup(&cfg)
			res, events := runCollect(t, cfg, nil, message.User("go"))
			if res.Reason != "done" {
				t.Fatalf("reason = %q, want done", res.Reason)
			}
			if countType(events, proto.EventExtensionError) == 0 {
				t.Fatalf("no extension_error for %s: %v", tc.name, eventTypes(events))
			}
		})
	}
}

func TestLPH06_HookExceedsDeadline(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, HookTimeout: 50 * time.Millisecond}
	started := make(chan struct{})
	cfg.Hooks.MessageEnd = func(ctx context.Context, m message.Message) message.Message {
		close(started)
		<-ctx.Done()
		return m
	}
	res, events := runCollect(t, cfg, nil, message.User("go"))
	<-started
	if res.Reason != "done" {
		t.Fatalf("reason = %q, want done", res.Reason)
	}
	found := false
	for _, e := range events {
		if e.Type == proto.EventExtensionError && e.Error != nil && strings.Contains(e.Error.Message, "deadline") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no deadline extension_error: %v", eventTypes(events))
	}
}

func TestLPH07_HookHonoursCancellation(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	returned := make(chan struct{})
	started := make(chan struct{})
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.BeforeTurn = func(ctx context.Context, m []message.Message) ([]message.Message, error) {
		close(started)
		<-ctx.Done()
		close(returned)
		return m, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = New(cfg).Run(ctx, nil, message.User("go"))
		close(done)
	}()
	<-started
	cancel()
	<-done
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("hook did not observe cancellation")
	}
}

func TestLPH08_TransformContext(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.TransformContext = func(_ context.Context, msgs []message.Message) []message.Message {
		return append([]message.Message(nil), msgs[1:]...)
	}
	res, _ := runCollect(t, cfg, []message.Message{message.User("drop-me"), message.User("keep-me")}, message.User("prompt"))
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request recorded")
	}
	for _, m := range reqs[0].Messages {
		if m.Content == "drop-me" {
			t.Fatalf("provider saw the removed message: %+v", reqs[0].Messages)
		}
	}
	if !hasMessageContent(res.Messages, "drop-me") {
		t.Fatalf("stored transcript lost the removed message: %+v", res.Messages)
	}
}

func hasMessageContent(msgs []message.Message, want string) bool {
	for _, m := range msgs {
		if m.Content == want {
			return true
		}
	}
	return false
}

func TestLPH09_BeforeProviderRequest(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.BeforeProviderRequest = func(_ context.Context, req *provider.Request) {
		if req.ProviderOptions == nil {
			req.ProviderOptions = map[string]any{}
		}
		req.ProviderOptions["pinned"] = "yes"
	}
	runCollect(t, cfg, nil, message.User("go"))
	reqs := p.Requests()
	if len(reqs) != 1 || reqs[0].ProviderOptions["pinned"] != "yes" {
		t.Fatalf("request options = %+v", reqs)
	}
}

func TestLPH10_BeforeTurnReplacesTranscript(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	var onMessage []string
	cfg := Config{Provider: p, Model: "m", Clock: clock, OnMessage: func(m message.Message, _ *provider.Usage) error {
		onMessage = append(onMessage, m.Content)
		return nil
	}}
	replacement := []message.Message{message.System("replaced-system"), message.User("replaced-user")}
	cfg.Hooks.BeforeTurn = func(context.Context, []message.Message) ([]message.Message, error) {
		return replacement, nil
	}
	runCollect(t, cfg, nil, message.User("prompt"))
	reqs := p.Requests()
	if len(reqs) != 1 || len(reqs[0].Messages) != 2 || reqs[0].Messages[1].Content != "replaced-user" {
		t.Fatalf("provider saw %+v, want the replacement", reqs)
	}
	for _, c := range onMessage {
		if c == "replaced-user" {
			t.Fatalf("OnMessage saw the replacement: %v", onMessage)
		}
	}
}

func TestLPH11_BeforeTurnError(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.BeforeTurn = func(context.Context, []message.Message) ([]message.Message, error) {
		return nil, errors.New("no turn")
	}
	res, events, err := runCollectErr(t, cfg, nil, message.User("go"))
	if err == nil || res.Reason != "error" {
		t.Fatalf("err = %v reason = %q, want error", err, res.Reason)
	}
	assertRunBoundaries(t, events)
}

func TestLPH12_MessageEndReplacesAndForcesRole(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.MessageEnd = func(context.Context, message.Message) message.Message {
		return message.Message{Role: message.RoleUser, Content: "changed"}
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Answer != "changed" {
		t.Fatalf("answer = %q, want changed", res.Answer)
	}
	for _, m := range res.Messages {
		if m.Content == "changed" && m.Role != message.RoleAssistant {
			t.Fatalf("role = %q, want assistant", m.Role)
		}
	}
}

func TestLPH13_TurnEndEnd(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, toolTurn())
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	cfg.Hooks.TurnEnd = func(context.Context, Turn) TurnDecision { return TurnDecision{End: true} }
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Reason != "done" || res.Turns != 1 {
		t.Fatalf("result = %+v, want done at 1 turn", res)
	}
	if !hasToolContent(res.Messages, "echo:echoed") {
		t.Fatal("tool result missing")
	}
}

func TestLPH14_TurnEndContinue(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("first"), textTurn("second"))
	calls := 0
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.TurnEnd = func(context.Context, Turn) TurnDecision {
		calls++
		return TurnDecision{Continue: calls == 1}
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Turns != 2 || res.Answer != "second" {
		t.Fatalf("result = %+v, want 2 turns ending second", res)
	}
}

func TestLPH15_TurnEndInject(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("first"), textTurn("second"))
	var onMessage []string
	cfg := Config{Provider: p, Model: "m", Clock: clock, OnMessage: func(m message.Message, _ *provider.Usage) error {
		onMessage = append(onMessage, m.Content)
		return nil
	}}
	injected := []message.Message{message.User("i1"), message.User("i2")}
	calls := 0
	cfg.Hooks.TurnEnd = func(context.Context, Turn) TurnDecision {
		calls++
		if calls == 1 {
			return TurnDecision{Inject: injected}
		}
		return TurnDecision{}
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Turns != 2 {
		t.Fatalf("turns = %d, want 2", res.Turns)
	}
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	var got []string
	for _, m := range reqs[1].Messages {
		if strings.HasPrefix(m.Content, "i") {
			got = append(got, m.Content)
		}
	}
	if len(got) != 2 || got[0] != "i1" || got[1] != "i2" {
		t.Fatalf("injected = %v, want [i1 i2]", got)
	}
	count := 0
	for _, c := range onMessage {
		if c == "i1" || c == "i2" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("OnMessage injected count = %d, want 2 (%v)", count, onMessage)
	}
}

func TestLPH16_AgentEndCalledOnce(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	var mu sync.Mutex
	calls := 0
	var got *Result
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.AgentEnd = func(_ context.Context, r *Result) {
		mu.Lock()
		calls++
		got = r
		mu.Unlock()
	}
	runCollect(t, cfg, nil, message.User("go"))
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || got == nil || got.Reason != "done" {
		t.Fatalf("agent_end calls = %d result = %+v", calls, got)
	}
}

func TestLPH17_OnMessageErrorAborts(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, OnMessage: func(message.Message, *provider.Usage) error {
		return errors.New("no")
	}}
	res, events, err := runCollectErr(t, cfg, nil, message.User("go"))
	if err == nil || res.Reason != "error" {
		t.Fatalf("err = %v reason = %q", err, res.Reason)
	}
	assertRunBoundaries(t, events)
}

func TestLPH18_NilHooks(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, toolTurn(), textTurn("done"))
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	if res.Answer != "done" {
		t.Fatalf("answer = %q", res.Answer)
	}
}
