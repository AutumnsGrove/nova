package messages

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

func TestMessages_MSG27_MalformedEventFailsTheTurn(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"kept"}}`),
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo`),
		sse("message_stop", `{"type":"message_stop"}`),
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

// The API reports input and output tokens in different events and no total;
// the usage line and the ledger read the total.
func TestMessages_MSG28_TotalTokens(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":10}}}`),
		sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`),
		sse("message_stop", `{"type":"message_stop"}`),
	)
	resp, events, err := run(t, p, provider.Request{Model: "m"})
	if err != nil || resp.Usage.TotalTokens != 14 {
		t.Fatalf("usage = %+v err = %v, want a total of 14", resp.Usage, err)
	}
	last := provider.Usage{}
	for _, ev := range events {
		if ev.Type == provider.StreamUsage {
			last = *ev.Usage
		}
	}
	if last != resp.Usage {
		t.Errorf("last usage event = %+v, want %+v", last, resp.Usage)
	}
}

func TestMessages_MSG29_AssistantHistoryWithUnusableArguments(t *testing.T) {
	// A model that is cut off mid-call leaves arguments that are not JSON. The
	// transcript keeps them, and the next request must still encode.
	for name, args := range map[string]string{"cut off": `{"path":"a`, "empty": ``, "not an object": `null`} {
		t.Run(name, func(t *testing.T) {
			p, srv := newProvider(t, Config{}, sse("message_stop", `{"type":"message_stop"}`))
			_, _, err := run(t, p, provider.Request{Model: "m", Messages: []message.Message{
				message.User("go"),
				{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{
					ID: "c1", Type: "function", Function: message.FunctionCall{Name: "read", Arguments: args},
				}}},
				{Role: message.RoleTool, ToolCallID: "c1", Content: "bad arguments"},
			}})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			var body struct {
				Messages []struct {
					Content []struct {
						Type  string         `json:"type"`
						Input map[string]any `json:"input"`
					} `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(srv.Requests()[0].Body, &body); err != nil {
				t.Fatal(err)
			}
			use := body.Messages[1].Content[0]
			if use.Type != "tool_use" || use.Input == nil || len(use.Input) != 0 {
				t.Errorf("tool_use = %+v, want an empty input object", use)
			}
		})
	}
}

func TestMessages_MSG23_ToolChoiceRequiredIsAny(t *testing.T) {
	p, srv := newProvider(t, Config{}, sse("message_stop", `{"type":"message_stop"}`))
	_, _, _ = run(t, p, provider.Request{Model: "m", ToolChoice: "required"})
	var body map[string]any
	_ = json.Unmarshal(srv.Requests()[0].Body, &body)
	if got, _ := body["tool_choice"].(map[string]any); got["type"] != "any" {
		t.Fatalf("tool_choice = %v, want type any", body["tool_choice"])
	}
}

func TestMessages_MSG24_ToolUseWithoutIDGetsOne(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"read"}}`),
		sse("message_stop", `{"type":"message_stop"}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil || len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID == "" {
		t.Fatalf("resp = %+v err = %v, want a call with a generated id", resp, err)
	}
}

func TestMessages_MSG25_ErrorEventKeepsItsType(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("error", `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`),
	)
	_, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Code != "invalid_request_error" || perr.Message != "bad" {
		t.Fatalf("err = %v", err)
	}
}

func TestMessages_MSG26_CutStreamKeepsNoHalfCalls(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"read"}}`),
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`),
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
