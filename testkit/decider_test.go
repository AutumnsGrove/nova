package testkit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/provider"
)

func decisionRequest() decide.Request {
	return decide.Request{
		Model: "typesafe/jev-1.14",
		State: []decide.Source{{Source: "one", Text: "first"}, {Source: "two", Text: "second"}},
		Questions: map[string]decide.Question{
			"q": {Instructions: "pick", Criteria: map[string]string{"a": "one", "b": "two"}},
		},
	}
}

func TestDecider_PlaysTurnsInOrder(t *testing.T) {
	d := NewDecider(nil,
		DecisionTurn{Model: "build-1", Answers: map[string]decide.Answer{"q": {Choice: "a", Confidence: 0.7}}, Usage: provider.Usage{PromptTokens: 3}},
		DecisionTurn{Model: "build-2", Answers: map[string]decide.Answer{"q": {Choice: "b", Confidence: 0.9}}},
	)
	if d.Remaining() != 2 {
		t.Fatalf("Remaining = %d, want 2", d.Remaining())
	}
	first, err := d.Decide(context.Background(), decisionRequest())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if first.Model != "build-1" || first.Answers["q"].Choice != "a" || first.Usage.PromptTokens != 3 {
		t.Fatalf("first = %+v", first)
	}
	second, err := d.Decide(context.Background(), decisionRequest())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if second.Model != "build-2" || second.Answers["q"].Choice != "b" {
		t.Fatalf("second = %+v", second)
	}
	if len(d.Requests()) != 2 {
		t.Fatalf("Requests = %d, want 2", len(d.Requests()))
	}
}

func TestDecider_Exhausted(t *testing.T) {
	d := NewDecider(nil)
	_, err := d.Decide(context.Background(), decisionRequest())
	if err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("Decide error = %v, want a script-exhausted error", err)
	}
}

func TestDecider_RequestsAreDeepCopied(t *testing.T) {
	d := NewDecider(nil, DecisionTurn{})
	req := decisionRequest()
	if _, err := d.Decide(context.Background(), req); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	req.Questions["q"].Criteria["a"] = "mutated"
	req.State.([]decide.Source)[0].Text = "mutated"
	got := d.Requests()[0]
	if got.Questions["q"].Criteria["a"] == "mutated" {
		t.Error("recorded question criteria aliases the caller's map")
	}
	if got.State.([]decide.Source)[0].Text == "mutated" {
		t.Error("recorded state aliases the caller's slice")
	}
}

func TestDecider_DelayUsesTheClock(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	d := NewDecider(clock, DecisionTurn{Delay: time.Second})
	type result struct {
		resp *decide.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := d.Decide(context.Background(), decisionRequest())
		done <- result{resp, err}
	}()
	clock.BlockUntil(t, 1)
	select {
	case got := <-done:
		t.Fatalf("Decide returned before the clock advanced: %+v", got)
	default:
	}
	clock.Advance(time.Second)
	if got := <-done; got.err != nil {
		t.Fatalf("Decide: %v", got.err)
	}
}

func TestDecider_HangHonoursCancellation(t *testing.T) {
	d := NewDecider(nil, DecisionTurn{Hang: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Decide(ctx, decisionRequest())
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Decide error = %v, want context.Canceled", err)
	}
}
