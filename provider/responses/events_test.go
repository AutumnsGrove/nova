package responses

import (
	"errors"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
)

// A payload that is not JSON used to be skipped, so a damaged text delta
// vanished and the turn still ended as a success with a hole in the answer.
func TestResponses_RSP27_MalformedEventFailsTheTurn(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_text.delta", `{"type":"response.output_text.delta","delta":"kept"}`),
		sse("response.output_text.delta", `{"type":"response.output_text.delta","delta":"lost`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Retryable || !strings.Contains(perr.Message, "invalid JSON") {
		t.Fatalf("err = %v, want a non-retryable invalid-JSON error", err)
	}
	if resp == nil || resp.Message.Content != "kept" {
		t.Fatalf("partial = %+v, want the text before the damage", resp)
	}
}

func TestResponses_RSP28_TopLevelErrorCarriesCodeAndMessage(t *testing.T) {
	// This is the shape the API documents for an "error" event.
	p, _ := newProvider(t, Config{},
		sse("error", `{"type":"error","code":"rate_limit_exceeded","message":"slow down","param":null}`),
	)
	_, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Code != "rate_limit_exceeded" || perr.Message != "slow down" {
		t.Fatalf("err = %v, want the top-level code and message", err)
	}
}

func TestResponses_RSP24_IncompleteReasons(t *testing.T) {
	for reason, want := range map[string]string{
		"max_output_tokens": "length",
		"content_filter":    "content_filter",
		"something_new":     "stop",
	} {
		p, _ := newProvider(t, Config{},
			sse("response.incomplete", `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"`+reason+`"}}}`),
		)
		resp, _, err := run(t, p, provider.Request{Model: "m"})
		if err != nil || resp.FinishReason != want {
			t.Errorf("%s: finish %q err %v, want %q", reason, resp.FinishReason, err, want)
		}
	}
}

func TestResponses_RSP25_CutStreamIsRetryableAndKeepsNoHalfCalls(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"c1","name":"read"}}`),
		sse("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"a\":"}`),
	)
	resp, events, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable {
		t.Fatalf("err = %v, want retryable", err)
	}
	if len(resp.Message.ToolCalls) != 0 {
		t.Errorf("a truncated call was returned: %+v", resp.Message.ToolCalls)
	}
	for _, ev := range events {
		if ev.Type == provider.StreamToolCall {
			t.Errorf("a truncated call was delivered: %+v", ev.ToolCall)
		}
	}
}

func TestResponses_RSP26_ToolCallWithoutCallIDGetsOne(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"read"}}`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil || len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID == "" {
		t.Fatalf("resp = %+v err = %v, want a call with a generated id", resp, err)
	}
}
