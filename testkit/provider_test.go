package testkit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// runTurn drives one Stream call and returns the response and the events.
func runTurn(t *testing.T, p *Provider, ctx context.Context, req provider.Request) (*provider.Response, []provider.StreamEvent, error) {
	t.Helper()
	var events []provider.StreamEvent
	resp, err := p.Stream(ctx, req, func(ev provider.StreamEvent) { events = append(events, ev) })
	return resp, events, err
}

func eventTypes(events []provider.StreamEvent) []provider.StreamEventType {
	out := make([]provider.StreamEventType, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

func TestProvider_TKP01_TurnsConsumedInOrder(t *testing.T) {
	p := NewProvider(RealClock(), Turn{Text: "one"}, Turn{Text: "two"})
	for _, want := range []string{"one", "two"} {
		resp, _, err := runTurn(t, p, context.Background(), provider.Request{Model: "m"})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		if resp.Message.Content != want {
			t.Errorf("content = %q, want %q", resp.Message.Content, want)
		}
	}
}

func TestProvider_TKP02_ExhaustedScript(t *testing.T) {
	p := NewProvider(RealClock(), Turn{Text: "only"})
	if _, _, err := runTurn(t, p, context.Background(), provider.Request{}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, _, err := runTurn(t, p, context.Background(), provider.Request{})
	if err == nil || !strings.Contains(err.Error(), "script exhausted") {
		t.Errorf("second call error = %v, want script exhausted", err)
	}
}

func TestProvider_TKP03_RequestsRecordedAsDeepCopies(t *testing.T) {
	req := provider.Request{
		Model: "m",
		Messages: []message.Message{
			{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "c1"}}},
		},
		ProviderOptions: map[string]any{"p": map[string]any{"x": 1}},
	}
	p := NewProvider(RealClock(), Turn{Text: "ok"})
	if _, _, err := runTurn(t, p, context.Background(), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	req.Messages[0].ToolCalls = append(req.Messages[0].ToolCalls, message.ToolCall{ID: "c2"})
	req.ProviderOptions["p"].(map[string]any)["x"] = 99
	req.ProviderOptions["new"] = true

	recorded := p.Requests()[0]
	if len(recorded.Messages[0].ToolCalls) != 1 {
		t.Errorf("recorded ToolCalls = %v, want the original one", recorded.Messages[0].ToolCalls)
	}
	if got := recorded.ProviderOptions["p"].(map[string]any)["x"]; got != 1 {
		t.Errorf("recorded nested option = %v, want 1", got)
	}
	if _, ok := recorded.ProviderOptions["new"]; ok {
		t.Errorf("recorded request gained a key added after the call")
	}
}

func TestProvider_TKP04_EventOrder(t *testing.T) {
	p := NewProvider(RealClock(), Turn{
		Reasoning: "thinking",
		Text:      "answer",
		ToolCalls: []Call{{ID: "c1", Name: "read", Args: `{"path":"a"}`}},
		Usage:     &provider.Usage{PromptTokens: 1},
	})
	_, events, err := runTurn(t, p, context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []provider.StreamEventType{
		provider.StreamReasoningDelta,
		provider.StreamTextDelta,
		provider.StreamToolCall,
		provider.StreamUsage,
	}
	got := eventTypes(events)
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestProvider_TKP05_SplitArgsOneEvent(t *testing.T) {
	p := NewProvider(RealClock(), Turn{
		ToolCalls: []Call{{ID: "c1", Name: "read", SplitArgs: []string{`{"a":`, "1", "}"}}},
	})
	resp, events, err := runTurn(t, p, context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(events) != 1 || events[0].Type != provider.StreamToolCall {
		t.Fatalf("events = %v, want one tool call", eventTypes(events))
	}
	if got := events[0].ToolCall.Function.Arguments; got != `{"a":1}` {
		t.Errorf("arguments = %q, want joined fragments", got)
	}
	if got := resp.Message.ToolCalls[0].Function.Arguments; got != `{"a":1}` {
		t.Errorf("message arguments = %q, want joined fragments", got)
	}
}

func TestProvider_TKP06_TextChunks(t *testing.T) {
	p := NewProvider(RealClock(), Turn{TextChunks: []string{"a", "b", "c"}})
	resp, events, err := runTurn(t, p, context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %v, want 3 text deltas", eventTypes(events))
	}
	if resp.Message.Content != "abc" {
		t.Errorf("content = %q, want abc", resp.Message.Content)
	}
}

func TestProvider_TKP07_ErrAfterN(t *testing.T) {
	boom := errors.New("boom")
	p := NewProvider(RealClock(), Turn{TextChunks: []string{"a", "b", "c"}, Err: boom, ErrAfter: 2})
	_, events, err := runTurn(t, p, context.Background(), provider.Request{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if len(events) != 2 {
		t.Errorf("events = %v, want exactly 2 before the error", events)
	}
}

func TestProvider_TKP08_HangCancellation(t *testing.T) {
	p := NewProvider(RealClock(), Turn{Hang: true})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	done := make(chan error, 1)
	go func() {
		_, err := p.Stream(ctx, provider.Request{}, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Hang did not return after cancel")
	}
}

func TestProvider_TKP09_DelayUsesClock(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p := NewProvider(clock, Turn{Text: "late", Delay: 5 * time.Second})
	done := make(chan *provider.Response, 1)
	go func() {
		resp, err := p.Stream(context.Background(), provider.Request{}, nil)
		if err != nil {
			t.Errorf("Stream: %v", err)
		}
		done <- resp
	}()
	clock.BlockUntil(t, 1)
	select {
	case <-done:
		t.Fatal("Stream returned before the delay elapsed")
	default:
	}
	clock.Advance(5 * time.Second)
	select {
	case resp := <-done:
		if resp.Message.Content != "late" {
			t.Errorf("content = %q, want late", resp.Message.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stream did not return after Advance")
	}
}

func TestProvider_TKP10_DelayCancellation(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	p := NewProvider(clock, Turn{Text: "never", Delay: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Stream(ctx, provider.Request{}, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestProvider_TKP11_DefaultFinish(t *testing.T) {
	text := NewProvider(RealClock(), Turn{Text: "hi"})
	resp, _, err := runTurn(t, text, context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("text-only finish = %q, want stop", resp.FinishReason)
	}
	calls := NewProvider(RealClock(), Turn{ToolCalls: []Call{{ID: "c", Name: "x"}}})
	resp, _, err = runTurn(t, calls, context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("tool-call finish = %q, want tool_calls", resp.FinishReason)
	}
}

func TestProvider_TKP12_NilOnEvent(t *testing.T) {
	p := NewProvider(RealClock(), Turn{Text: "ok"})
	if _, err := p.Stream(context.Background(), provider.Request{}, nil); err != nil {
		t.Errorf("Stream with nil onEvent = %v, want nil", err)
	}
}

func TestProvider_TKP13_ConcurrentCallsSafe(t *testing.T) {
	turns := make([]Turn, 10)
	for i := range turns {
		turns[i] = Turn{Text: fmt.Sprintf("turn-%d", i)}
	}
	p := NewProvider(RealClock(), turns...)

	var mu sync.Mutex
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := p.Stream(context.Background(), provider.Request{}, nil)
			if err != nil {
				t.Errorf("Stream: %v", err)
				return
			}
			mu.Lock()
			seen[resp.Message.Content] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != 10 {
		t.Errorf("saw %d distinct turns, want 10", len(seen))
	}
	for i := 0; i < 10; i++ {
		if !seen[fmt.Sprintf("turn-%d", i)] {
			t.Errorf("turn-%d was not returned", i)
		}
	}
}

func TestProvider_TKP14_Remaining(t *testing.T) {
	p := NewProvider(RealClock(), Turn{Text: "a"}, Turn{Text: "b"})
	if got := p.Remaining(); got != 2 {
		t.Errorf("Remaining = %d, want 2", got)
	}
	if _, _, err := runTurn(t, p, context.Background(), provider.Request{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := p.Remaining(); got != 1 {
		t.Errorf("Remaining = %d, want 1", got)
	}
}
