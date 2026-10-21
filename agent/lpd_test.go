package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// decisionRequest is the question a test tool asks.
func decisionRequest() decide.Request {
	return decide.Request{
		Model: "vendor/model",
		State: "the evidence",
		Questions: map[string]decide.Question{
			"q": {Instructions: "pick", Criteria: map[string]string{"a": "one", "b": "two"}},
		},
	}
}

func TestDecide_ToolReachesTheDeciderAndAddsUsage(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, namedToolTurn("triage_ask"), textTurn("done"))
	decider := testkit.NewDecider(clock, testkit.DecisionTurn{
		Model:   "build-1",
		Answers: map[string]decide.Answer{"q": {Choice: "a", Confidence: 0.9}},
		Usage:   provider.Usage{PromptTokens: 7, CostUSD: 0.01},
	})
	var got *decide.Response
	cfg := Config{
		Provider: p,
		Model:    "m",
		Clock:    clock,
		Decide:   decider.Decide,
		Tools: registry(toolOf("triage_ask", false, func(ctx context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
			if tctx.Decide == nil {
				return tool.Errorf("no decide seam"), nil
			}
			resp, err := tctx.Decide(ctx, decisionRequest())
			if err != nil {
				return tool.Errorf("decide: %v", err), nil
			}
			got = resp
			tctx.AddUsage(resp.Usage)
			return tool.Text("chose %s", resp.Answers["q"].Choice), nil
		})),
	}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Reason != "done" {
		t.Fatalf("reason = %q, want done", res.Reason)
	}
	if got == nil || got.Model != "build-1" || got.Answers["q"].Choice != "a" {
		t.Fatalf("tool got %+v, want the scripted answer", got)
	}
	if res.Usage.PromptTokens != 7 || res.Usage.CostUSD != 0.01 {
		t.Errorf("run usage = %+v, want the decision call counted", res.Usage)
	}
	reqs := decider.Requests()
	if len(reqs) != 1 || reqs[0].Model != "vendor/model" {
		t.Fatalf("decider requests = %+v, want one", reqs)
	}
}

func TestDecide_HookIsBoundedByHookTimeout(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	decider := testkit.NewDecider(nil, testkit.DecisionTurn{Hang: true})
	cfg := Config{
		Provider:    p,
		Model:       "m",
		Clock:       clock,
		HookTimeout: 50 * time.Millisecond,
		Decide:      decider.Decide,
	}
	called := make(chan struct{}, 1)
	cfg.Hooks.TurnEnd = func(ctx context.Context, _ Turn) TurnDecision {
		called <- struct{}{}
		_, _ = cfg.Decide(ctx, decisionRequest())
		return TurnDecision{}
	}
	res, events := runCollect(t, cfg, nil, message.User("go"))
	select {
	case <-called:
	default:
		t.Fatal("TurnEnd hook did not run")
	}
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
