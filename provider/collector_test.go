package provider

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
)

// record collects the events a Collector delivers.
func record(events *[]StreamEvent) func(StreamEvent) {
	return func(ev StreamEvent) { *events = append(*events, ev) }
}

func TestProvider_PRV23_CollectorTextReasoningUsage(t *testing.T) {
	var events []StreamEvent
	c := NewCollector("req-model", record(&events))
	c.Text("Hel")
	c.Text("")
	c.Text("lo")
	c.Reasoning("hm")
	c.Usage(Usage{PromptTokens: 3})
	c.Usage(Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5})
	c.SetModel("")
	c.SetModel("served-model")

	r := c.Finish("end_turn")
	if r.Message.Content != "Hello" || r.Message.Reasoning != "hm" || r.Message.Role != message.RoleAssistant {
		t.Errorf("message = %+v", r.Message)
	}
	if r.Model != "served-model" || r.FinishReason != "stop" {
		t.Errorf("model %q finish %q", r.Model, r.FinishReason)
	}
	if r.Usage.TotalTokens != 5 {
		t.Errorf("usage = %+v, want the latest report", r.Usage)
	}
	want := []StreamEventType{StreamTextDelta, StreamTextDelta, StreamReasoningDelta, StreamUsage, StreamUsage}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %d (an empty delta is not an event)", events, len(want))
	}
	for i, ev := range events {
		if ev.Type != want[i] {
			t.Errorf("event %d = %s, want %s", i, ev.Type, want[i])
		}
	}
}

func TestProvider_PRV24_CollectorNilCallback(t *testing.T) {
	c := NewCollector("m", nil)
	c.Text("x")
	c.Reasoning("y")
	c.Usage(Usage{})
	c.ToolCall(0, "id", "", "run", "{}")
	if r := c.Finish("tool_calls"); len(r.Message.ToolCalls) != 1 {
		t.Fatalf("response = %+v", r)
	}
}

func TestProvider_PRV25_CollectorToolCallAssembly(t *testing.T) {
	var events []StreamEvent
	c := NewCollector("m", record(&events))
	// Fragments for two calls arrive interleaved, the second index first, and
	// the id of the first call only arrives late.
	c.ToolCall(1, "b", "", "second", `{"y"`)
	c.ToolCall(0, "", "", "first", `{"x"`)
	c.ToolCall(1, "", "", "", `:2}`)
	c.ToolCall(0, "a", "", "", `:1}`)
	c.ToolCall(0, "other", "", "renamed", ``) // first non-empty value wins

	if len(events) != 0 {
		t.Fatalf("events before Finish = %+v, want none", events)
	}
	if got := c.Partial().Message.ToolCalls; got != nil {
		t.Fatalf("Partial carries tool calls %+v", got)
	}
	r := c.Finish("stop")
	if len(r.Message.ToolCalls) != 2 {
		t.Fatalf("calls = %+v", r.Message.ToolCalls)
	}
	first, second := r.Message.ToolCalls[0], r.Message.ToolCalls[1]
	if first.ID != "a" || first.Function.Name != "first" || first.Function.Arguments != `{"x":1}` || first.Type != "function" {
		t.Errorf("first = %+v", first)
	}
	if second.ID != "b" || second.Function.Name != "second" || second.Function.Arguments != `{"y":2}` {
		t.Errorf("second = %+v", second)
	}
	if r.FinishReason != "tool_calls" {
		t.Errorf("finish = %q, want a stop with calls reported as tool_calls", r.FinishReason)
	}
	if len(events) != 2 || events[0].ToolCall.ID != "a" || events[1].ToolCall.ID != "b" {
		t.Errorf("events = %+v, want both calls in index order", events)
	}
}

func TestProvider_PRV26_CollectorIDs(t *testing.T) {
	c := NewCollector("m", nil)
	c.ToolCall(0, "dup", "", "a", "")
	c.ToolCall(1, "dup", "", "b", "") // a repeated id would make two results answer each other
	c.ToolCall(2, "", "", "c", "")
	c.ToolCall(3, "call_2_0", "", "d", "") // a provider id that looks generated
	calls := c.Finish("").Message.ToolCalls
	if len(calls) != 4 {
		t.Fatalf("calls = %+v", calls)
	}
	seen := map[string]bool{}
	for _, call := range calls {
		if call.ID == "" || seen[call.ID] {
			t.Errorf("id %q is empty or repeated in %+v", call.ID, calls)
		}
		seen[call.ID] = true
	}
	if calls[0].ID != "dup" || calls[3].ID != "call_2_0" {
		t.Errorf("provider ids were not kept: %+v", calls)
	}
	if !strings.HasPrefix(calls[2].ID, "call_2_") {
		t.Errorf("generated id = %q, want call_<index>_<n>", calls[2].ID)
	}
}

func TestProvider_PRV27_CollectorDropsNamelessCalls(t *testing.T) {
	var events []StreamEvent
	c := NewCollector("m", record(&events))
	c.ToolCall(0, "x", "", "", `{}`)
	c.ToolCall(1, "y", "", "real", "")
	r := c.Finish("tool_calls")
	if len(r.Message.ToolCalls) != 1 || r.Message.ToolCalls[0].Function.Arguments != "{}" {
		t.Fatalf("calls = %+v, want the named one with empty arguments as {}", r.Message.ToolCalls)
	}
	if len(events) != 2 || events[0].Type != StreamError || events[1].Type != StreamToolCall {
		t.Errorf("events = %+v, want the drop reported before the call", events)
	}
}

func TestProvider_PRV28_CollectorFinishReasons(t *testing.T) {
	for _, tc := range []struct {
		reason string
		calls  bool
		want   string
	}{
		{"stop", false, "stop"},
		{"length", false, "length"},
		{"length", true, "length"}, // a cut-off call list is still a cut-off
		{"end_turn", true, "tool_calls"},
		{"", true, "tool_calls"},
		{"content_filter", false, "content_filter"},
		{"mystery", false, ""},
		{"", false, ""},
	} {
		c := NewCollector("m", nil)
		if tc.calls {
			c.ToolCall(0, "id", "", "run", "")
		}
		if got := c.Finish(tc.reason).FinishReason; got != tc.want {
			t.Errorf("Finish(%q) with calls=%v = %q, want %q", tc.reason, tc.calls, got, tc.want)
		}
	}
}
