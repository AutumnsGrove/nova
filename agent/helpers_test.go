package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// baseTime is the fixed instant every fake clock starts at, so event TS values
// are deterministic.
var baseTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// collector records every emitted event under a mutex.
type collector struct {
	mu     sync.Mutex
	events []proto.Event
}

func (c *collector) emit(e proto.Event) {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
}

func (c *collector) all() []proto.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]proto.Event(nil), c.events...)
}

func (c *collector) types() []string { return eventTypes(c.all()) }

func (c *collector) count(typ string) int {
	n := 0
	for _, e := range c.all() {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func (c *collector) filter(typ string) []proto.Event {
	var out []proto.Event
	for _, e := range c.all() {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func (c *collector) find(typ string) (proto.Event, bool) {
	for _, e := range c.all() {
		if e.Type == typ {
			return e, true
		}
	}
	return proto.Event{}, false
}

// eventTypes maps events to their type strings, for order assertions.
func eventTypes(events []proto.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

// runCollect runs one agent and returns its result and every event.
func runCollect(t *testing.T, cfg Config, history []message.Message, prompt message.Message) (*Result, []proto.Event) {
	t.Helper()
	c := &collector{}
	cfg.Emit = c.emit
	res, err := New(cfg).Run(context.Background(), history, prompt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res, c.all()
}

// runCollectErr is runCollect for runs expected to fail.
func runCollectErr(t *testing.T, cfg Config, history []message.Message, prompt message.Message) (*Result, []proto.Event, error) {
	t.Helper()
	c := &collector{}
	cfg.Emit = c.emit
	res, err := New(cfg).Run(context.Background(), history, prompt)
	return res, c.all(), err
}

// assertRunBoundaries checks the invariant every scenario must keep: exactly
// one run_start, exactly one run_end, and nothing after run_end.
func assertRunBoundaries(t *testing.T, events []proto.Event) {
	t.Helper()
	if got := countType(events, proto.EventRunStart); got != 1 {
		t.Errorf("run_start count = %d, want 1", got)
	}
	if got := countType(events, proto.EventRunEnd); got != 1 {
		t.Errorf("run_end count = %d, want 1", got)
	}
	for i, e := range events {
		if e.Type == proto.EventRunEnd && i != len(events)-1 {
			t.Errorf("event after run_end at %d: %s", i, e.Type)
			return
		}
	}
}

func countType(events []proto.Event, typ string) int {
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// registry builds a registry from tools, panicking only on a test wiring bug.
func registry(tools ...tool.Tool) *tool.Registry {
	r := tool.NewRegistry()
	for _, tl := range tools {
		if err := r.Register(tl); err != nil {
			panic(err)
		}
	}
	return r
}

// toolOf builds a tool with the given parallel annotation.
func toolOf(name string, parallel bool, fn tool.Func) tool.Tool {
	return tool.New(tool.Info{
		Name:        name,
		Description: name,
		Parameters:  map[string]any{"type": "object"},
		Parallel:    parallel,
	}, fn)
}

// staticTool returns a fixed text result.
func staticTool(name, text string) tool.Tool {
	return toolOf(name, false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Text("%s", text), nil
	})
}

// toolCall builds an assistant tool-call message for a scripted turn.
func toolCall(id, name, args string) testkit.Call {
	return testkit.Call{ID: id, Name: name, Args: args}
}

// alwaysTool builds n turns that each call the tool.
func alwaysTool(n int) []testkit.Turn {
	turns := make([]testkit.Turn, n)
	for i := range turns {
		turns[i] = testkit.Turn{ToolCalls: []testkit.Call{toolCall("c", "echo", `{}`)}}
	}
	return turns
}

// echoTool answers every call with its argument's "text" field, or "echoed".
func echoTool() tool.Tool {
	return toolOf("echo", true, func(_ context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
		var in struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(args, &in)
		if in.Text == "" {
			in.Text = "echoed"
		}
		return tool.Text("echo:%s", in.Text), nil
	})
}

// advancingProvider emits reasoning, advances the fake clock, then text, so the
// thinking duration is exactly the advance.
type advancingProvider struct {
	clock *testkit.FakeClock
	text  string
}

func (p *advancingProvider) Name() string { return "advancing" }

func (p *advancingProvider) Stream(_ context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	onEvent(provider.StreamEvent{Type: provider.StreamReasoningDelta, Text: "think"})
	p.clock.Advance(3 * time.Second)
	onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: p.text})
	return &provider.Response{Message: message.Message{
		Role:      message.RoleAssistant,
		Content:   p.text,
		Reasoning: "think",
	}}, nil
}

// hangingTool blocks until its context is cancelled, signalling when it starts.
func hangingTool(started chan<- struct{}) tool.Tool {
	return toolOf("hang", false, func(ctx context.Context, _ json.RawMessage, _ *tool.Context) (tool.Result, error) {
		if started != nil {
			started <- struct{}{}
		}
		<-ctx.Done()
		return tool.Result{}, ctx.Err()
	})
}
