package messages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

func sse(typ, payload string) string {
	return "event: " + typ + "\ndata: " + payload + "\n\n"
}

func newProvider(t *testing.T, cfg Config, raw ...string) (*Provider, *testkit.Server) {
	t.Helper()
	if raw == nil {
		raw = []string{}
	}
	srv := testkit.NewServer(t, testkit.ServerTurn{Raw: raw})
	cfg.BaseURL = srv.URL
	return New(cfg), srv
}

func run(t *testing.T, p *Provider, req provider.Request) (*provider.Response, []provider.StreamEvent, error) {
	t.Helper()
	var events []provider.StreamEvent
	resp, err := p.Stream(context.Background(), req, func(ev provider.StreamEvent) { events = append(events, ev) })
	return resp, events, err
}

func TestMessages_MSG01_PlainText(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":5}}}`),
		sse("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`),
		sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`),
		sse("message_stop", `{"type":"message_stop"}`),
	)
	resp, events, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "Hi" || resp.FinishReason != "stop" {
		t.Fatalf("resp = %+v", resp)
	}
	texts := 0
	for _, ev := range events {
		if ev.Type == provider.StreamTextDelta {
			texts++
		}
	}
	if texts != 1 {
		t.Fatalf("text deltas = %d", texts)
	}
}

func TestMessages_MSG02_RequestBodyShape(t *testing.T) {
	p, srv := newProvider(t, Config{},
		sse("message_stop", `{"type":"message_stop"}`),
	)
	_, _, err := run(t, p, provider.Request{
		Model:    "m",
		Messages: []message.Message{{Role: message.RoleSystem, Content: "sys"}, {Role: message.RoleUser, Content: "hi"}},
		Tools:    []provider.ToolDef{{Type: "function", Function: provider.ToolFunction{Name: "t", Parameters: map[string]any{"type": "object"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(srv.Requests()[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"model", "system", "messages", "tools", "max_tokens"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("body missing %q: %v", key, body)
		}
	}
	if body["stream"] != true {
		t.Fatalf("stream = %v", body["stream"])
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	if tool["input_schema"] == nil {
		t.Fatalf("tool = %v, want input_schema", tool)
	}
}

func TestMessages_MSG03_Headers(t *testing.T) {
	p, srv := newProvider(t, Config{APIKey: "k"},
		sse("message_stop", `{"type":"message_stop"}`),
	)
	_, _, _ = run(t, p, provider.Request{Model: "m"})
	header := srv.Requests()[0].Header
	if header.Get("x-api-key") != "k" {
		t.Fatalf("x-api-key = %q", header.Get("x-api-key"))
	}
	if header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("anthropic-version = %q", header.Get("anthropic-version"))
	}
}

func TestMessages_MSG04_MaxTokensAlwaysPresent(t *testing.T) {
	p, srv := newProvider(t, Config{MaxTokens: 777},
		sse("message_stop", `{"type":"message_stop"}`),
	)
	_, _, _ = run(t, p, provider.Request{Model: "m"})
	var body map[string]any
	_ = json.Unmarshal([]byte(srv.Requests()[0].Body), &body)
	if body["max_tokens"].(float64) != 777 {
		t.Fatalf("max_tokens = %v", body["max_tokens"])
	}
}

func TestMessages_MSG05_Thinking(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`),
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`),
		sse("message_stop", `{"type":"message_stop"}`),
	)
	resp, events, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Reasoning != "hmm" {
		t.Fatalf("reasoning = %q", resp.Message.Reasoning)
	}
	found := false
	for _, ev := range events {
		if ev.Type == provider.StreamReasoningDelta {
			found = true
		}
	}
	if !found {
		t.Fatal("no reasoning event")
	}
}

func TestMessages_MSG06_ToolUseFragments(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"read"}}`),
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\""}}`),
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":1}"}}`),
		sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`),
		sse("message_stop", `{"type":"message_stop"}`),
	)
	resp, events, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Function.Arguments != `{"a":1}` {
		t.Fatalf("calls = %+v", resp.Message.ToolCalls)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish = %q", resp.FinishReason)
	}
	if events[len(events)-1].Type != provider.StreamToolCall {
		t.Fatalf("tool call not last: %v", events)
	}
}

func TestMessages_MSG07_TwoToolBlocks(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"b","name":"second"}}`),
		sse("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"first"}}`),
		sse("message_stop", `{"type":"message_stop"}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 2 || resp.Message.ToolCalls[0].Function.Name != "first" || resp.Message.ToolCalls[1].Function.Name != "second" {
		t.Fatalf("calls = %+v", resp.Message.ToolCalls)
	}
}

func TestMessages_MSG08_ToolResultRoundTrip(t *testing.T) {
	p, srv := newProvider(t, Config{},
		sse("message_stop", `{"type":"message_stop"}`),
	)
	_, _, _ = run(t, p, provider.Request{Model: "m", Messages: []message.Message{
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "t1", Function: message.FunctionCall{Name: "read", Arguments: `{"p":1}`}}}},
		{Role: message.RoleTool, ToolCallID: "t1", Content: "ok"},
	}})
	var body struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal([]byte(srv.Requests()[0].Body), &body)
	if body.Messages[0].Content[0]["type"] != "tool_use" {
		t.Fatalf("tool_use = %v", body.Messages[0].Content)
	}
	if body.Messages[1].Content[0]["type"] != "tool_result" || body.Messages[1].Content[0]["tool_use_id"] != "t1" {
		t.Fatalf("tool_result = %v", body.Messages[1].Content)
	}
}

func TestMessages_MSG09_ToolChoiceMapping(t *testing.T) {
	cases := []struct {
		name   string
		choice any
		want   map[string]any
	}{
		{"auto", "auto", map[string]any{"type": "auto"}},
		{"named", map[string]any{"function": map[string]any{"name": "read"}}, map[string]any{"type": "tool", "name": "read"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, srv := newProvider(t, Config{}, sse("message_stop", `{"type":"message_stop"}`))
			_, _, _ = run(t, p, provider.Request{Model: "m", ToolChoice: tc.choice})
			var body map[string]any
			_ = json.Unmarshal([]byte(srv.Requests()[0].Body), &body)
			got := body["tool_choice"].(map[string]any)
			if got["type"] != tc.want["type"] || got["name"] != tc.want["name"] {
				t.Fatalf("tool_choice = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMessages_MSG10_UsageAcrossEvents(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":2}}}`),
		sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`),
		sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`),
		sse("message_stop", `{"type":"message_stop"}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CachedTokens != 2 || resp.Usage.CompletionTokens != 7 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestMessages_MSG11_StopReasonMapping(t *testing.T) {
	cases := map[string]string{
		"end_turn":      "stop",
		"tool_use":      "tool_calls",
		"max_tokens":    "length",
		"stop_sequence": "stop",
	}
	for reason, want := range cases {
		p, _ := newProvider(t, Config{},
			sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+reason+`"}}`),
			sse("message_stop", `{"type":"message_stop"}`),
		)
		resp, _, err := run(t, p, provider.Request{Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		if resp.FinishReason != want {
			t.Fatalf("%s -> %q, want %q", reason, resp.FinishReason, want)
		}
	}
}

func TestMessages_MSG12_PingIgnored(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("ping", `{"type":"ping"}`),
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`),
		sse("message_stop", `{"type":"message_stop"}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil || resp.Message.Content != "x" {
		t.Fatalf("resp = %+v err = %v", resp, err)
	}
}

func TestMessages_MSG13_ErrorEvent(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`),
	)
	_, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Retryable || perr.Message != "Overloaded" {
		t.Fatalf("err = %v", err)
	}
}

func TestMessages_MSG14_EndsBeforeStop(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable {
		t.Fatalf("err = %v, want retryable", err)
	}
	if resp == nil || resp.Message.Content != "partial" {
		t.Fatalf("partial = %+v", resp)
	}
}

func TestMessages_MSG15_EmptyStream(t *testing.T) {
	p, _ := newProvider(t, Config{})
	_, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable || !strings.Contains(perr.Message, "empty stream") {
		t.Fatalf("err = %v", err)
	}
}

func TestMessages_MSG16_ConnectTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	p := New(Config{BaseURL: srv.URL, ConnectTimeout: 50 * time.Millisecond})
	_, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable {
		t.Fatalf("err = %v", err)
	}
}

func TestMessages_MSG17_IdleTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	p := New(Config{BaseURL: srv.URL, IdleTimeout: 50 * time.Millisecond})
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable {
		t.Fatalf("err = %v", err)
	}
	if resp == nil || resp.Message.Content != "hi" {
		t.Fatalf("partial = %+v", resp)
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// FuzzMessagesStream feeds arbitrary bytes as a Messages SSE body.
func FuzzMessagesStream(f *testing.F) {
	f.Add([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n"))
	f.Add([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	f.Add([]byte("not sse at all"))
	f.Fuzz(func(t *testing.T, data []byte) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
		})}
		p := New(Config{BaseURL: "http://example.invalid", HTTPClient: client})
		_, _ = p.Stream(context.Background(), provider.Request{Model: "m"}, nil)
	})
}
