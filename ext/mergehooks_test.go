package ext

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// Two Env.Hooks calls from one bundled extension compose: a rewrite by the
// first must survive when the second has no opinion.
func TestMergeHooks_IPC13_RewriteSurvivesLaterNoOpinion(t *testing.T) {
	var merged agent.Hooks
	mergeHooks(&merged, agent.Hooks{ToolCall: func(context.Context, agent.ToolCall) *agent.CallDecision {
		return &agent.CallDecision{Arguments: json.RawMessage(`{"a":1}`)}
	}})
	var seen json.RawMessage
	mergeHooks(&merged, agent.Hooks{ToolCall: func(_ context.Context, c agent.ToolCall) *agent.CallDecision {
		seen = c.Arguments
		return nil
	}})
	dec := merged.ToolCall(context.Background(), agent.ToolCall{Name: "x", Arguments: json.RawMessage(`{}`)})
	if string(seen) != `{"a":1}` {
		t.Fatalf("second hook saw %s, want the first hook's rewrite", seen)
	}
	if dec == nil || string(dec.Arguments) != `{"a":1}` {
		t.Fatalf("decision = %+v, want the first hook's rewrite", dec)
	}
}

func TestMergeHooks_IPC13_BlockWinsAndStopsTheChain(t *testing.T) {
	var merged agent.Hooks
	mergeHooks(&merged, agent.Hooks{ToolCall: func(context.Context, agent.ToolCall) *agent.CallDecision {
		return &agent.CallDecision{Block: true, Reason: "no"}
	}})
	called := false
	mergeHooks(&merged, agent.Hooks{ToolCall: func(context.Context, agent.ToolCall) *agent.CallDecision {
		called = true
		return nil
	}})
	dec := merged.ToolCall(context.Background(), agent.ToolCall{})
	if dec == nil || !dec.Block || called {
		t.Fatalf("decision = %+v, second hook called = %v", dec, called)
	}
}

func TestMergeHooks_IPC13_ChainsRunInOrder(t *testing.T) {
	var merged agent.Hooks
	var order []string
	note := func(name string) agent.Hooks {
		return agent.Hooks{
			BeforeProviderRequest: func(context.Context, *provider.Request) { order = append(order, name) },
			TransformContext: func(_ context.Context, m []message.Message) []message.Message {
				return append(m, message.User(name))
			},
			AgentEnd: func(context.Context, *agent.Result) { order = append(order, name) },
			TurnEnd: func(context.Context, agent.Turn) agent.TurnDecision {
				return agent.TurnDecision{Inject: []message.Message{message.User(name)}, Continue: name == "b"}
			},
		}
	}
	mergeHooks(&merged, note("a"))
	mergeHooks(&merged, note("b"))
	merged.BeforeProviderRequest(context.Background(), &provider.Request{})
	merged.AgentEnd(context.Background(), &agent.Result{})
	if len(order) != 4 || order[0] != "a" || order[1] != "b" || order[2] != "a" || order[3] != "b" {
		t.Fatalf("order = %v", order)
	}
	if got := merged.TransformContext(context.Background(), nil); len(got) != 2 {
		t.Fatalf("transform_context chained %d messages, want 2", len(got))
	}
	dec := merged.TurnEnd(context.Background(), agent.Turn{})
	if !dec.Continue || dec.End || len(dec.Inject) != 2 {
		t.Fatalf("turn_end = %+v, want Continue and two injected messages", dec)
	}
}
