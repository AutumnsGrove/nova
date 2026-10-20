package responses

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

// sse frames one event.
func sse(typ string, payload string) string {
	return "event: " + typ + "\ndata: " + payload + "\n\n"
}

// newProvider serves the given raw SSE chunks.
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

func TestResponses_RSP01_PlainText(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_text.delta", `{"type":"response.output_text.delta","delta":"Hel"}`),
		sse("response.output_text.delta", `{"type":"response.output_text.delta","delta":"lo "}`),
		sse("response.output_text.delta", `{"type":"response.output_text.delta","delta":"world"}`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, events, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "Hello world" {
		t.Fatalf("content = %q", resp.Message.Content)
	}
	texts := 0
	for _, ev := range events {
		if ev.Type == provider.StreamTextDelta {
			texts++
		}
	}
	if texts != 3 {
		t.Fatalf("text deltas = %d, want 3", texts)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("finish = %q, want stop", resp.FinishReason)
	}
}

func TestResponses_RSP02_RequestBodyShape(t *testing.T) {
	p, srv := newProvider(t, Config{MaxOutputTokens: 123},
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	temp := 0.5
	effort := "high"
	_, _, err := run(t, p, provider.Request{
		Model:           "m",
		Messages:        []message.Message{{Role: message.RoleUser, Content: "hi"}},
		Tools:           []provider.ToolDef{{Type: "function", Function: provider.ToolFunction{Name: "t"}}},
		Temperature:     &temp,
		ReasoningEffort: effort,
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(srv.Requests()[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"model", "input", "tools", "max_output_tokens", "reasoning"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("body missing %q: %v", key, body)
		}
	}
	if body["stream"] != true {
		t.Fatalf("stream = %v", body["stream"])
	}
	if body["max_output_tokens"].(float64) != 123 {
		t.Fatalf("max_output_tokens = %v", body["max_output_tokens"])
	}
	if body["reasoning"].(map[string]any)["effort"] != "high" {
		t.Fatalf("reasoning = %v", body["reasoning"])
	}
}

func TestResponses_RSP03_SystemInstructions(t *testing.T) {
	p, srv := newProvider(t, Config{},
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	_, _, err := run(t, p, provider.Request{Model: "m", Messages: []message.Message{
		{Role: message.RoleSystem, Content: "be brief"},
		{Role: message.RoleUser, Content: "hi"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(srv.Requests()[0].Body), &body)
	if body["instructions"] != "be brief" {
		t.Fatalf("instructions = %v", body["instructions"])
	}
	input := body["input"].([]any)
	if len(input) != 1 || input[0].(map[string]any)["role"] != "user" {
		t.Fatalf("input = %v", input)
	}
}

func TestResponses_RSP04_CallAndResultRoundTrip(t *testing.T) {
	p, srv := newProvider(t, Config{},
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	_, _, err := run(t, p, provider.Request{Model: "m", Messages: []message.Message{
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "c1", Function: message.FunctionCall{Name: "read", Arguments: `{"p":1}`}}}},
		{Role: message.RoleTool, ToolCallID: "c1", Content: "ok"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Input []map[string]any `json:"input"`
	}
	_ = json.Unmarshal([]byte(srv.Requests()[0].Body), &body)
	if len(body.Input) != 2 {
		t.Fatalf("input = %v", body.Input)
	}
	if body.Input[0]["type"] != "function_call" || body.Input[0]["call_id"] != "c1" {
		t.Fatalf("call item = %v", body.Input[0])
	}
	if body.Input[1]["type"] != "function_call_output" || body.Input[1]["output"] != "ok" {
		t.Fatalf("result item = %v", body.Input[1])
	}
}

func TestResponses_RSP05_ReasoningDelta(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","delta":"think"}`),
		sse("response.reasoning_text.delta", `{"type":"response.reasoning_text.delta","delta":"ing"}`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, events, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Reasoning != "thinking" {
		t.Fatalf("reasoning = %q", resp.Message.Reasoning)
	}
	reasoning := 0
	for _, ev := range events {
		if ev.Type == provider.StreamReasoningDelta {
			reasoning++
		}
	}
	if reasoning != 2 {
		t.Fatalf("reasoning events = %d, want 2", reasoning)
	}
}

func TestResponses_RSP06_OneCallThreeFragments(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"i1","type":"function_call","call_id":"c1","name":"read"}}`),
		sse("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"i1","output_index":0,"delta":"{\"a\""}`),
		sse("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"i1","output_index":0,"delta":":1"}`),
		sse("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"i1","output_index":0,"delta":"}"}`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, events, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("calls = %v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "c1" || call.Function.Name != "read" || call.Function.Arguments != `{"a":1}` {
		t.Fatalf("call = %+v", call)
	}
	if len(events) == 0 || events[len(events)-1].Type != provider.StreamToolCall {
		t.Fatalf("tool call not emitted at the end: %v", events)
	}
}

func TestResponses_RSP07_TwoCallsIndexOrder(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"id":"b","type":"function_call","call_id":"cb","name":"second"}}`),
		sse("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"a","type":"function_call","call_id":"ca","name":"first"}}`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("calls = %v", resp.Message.ToolCalls)
	}
	if resp.Message.ToolCalls[0].Function.Name != "first" || resp.Message.ToolCalls[1].Function.Name != "second" {
		t.Fatalf("order = %v", resp.Message.ToolCalls)
	}
}

func TestResponses_RSP08_EmptyArguments(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"i1","type":"function_call","call_id":"c1","name":"read"}}`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.ToolCalls[0].Function.Arguments != "{}" {
		t.Fatalf("arguments = %q", resp.Message.ToolCalls[0].Function.Arguments)
	}
}

func TestResponses_RSP09_InvalidJSONArguments(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"i1","type":"function_call","call_id":"c1","name":"read"}}`),
		sse("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"i1","output_index":0,"delta":"{\"a\":"}`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.ToolCalls[0].Function.Arguments != `{"a":` {
		t.Fatalf("arguments = %q", resp.Message.ToolCalls[0].Function.Arguments)
	}
}

func TestResponses_RSP10_UsageMapping(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.completed", `{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":35,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":5}}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u.PromptTokens != 10 || u.CompletionTokens != 20 || u.TotalTokens != 35 || u.CachedTokens != 3 || u.ReasoningTokens != 5 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestResponses_RSP11_IncompleteMaxTokens(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.incomplete", `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != "length" {
		t.Fatalf("finish = %q, want length", resp.FinishReason)
	}
}

func TestResponses_RSP12_Failed(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.failed", `{"type":"response.failed","response":{"error":{"message":"boom"}}}`),
	)
	_, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Retryable || perr.Message != "boom" {
		t.Fatalf("err = %v, want a non-retryable boom", err)
	}
}

// A failure that arrives inside the stream is retried when it is the kind that
// clears on its own, like the same failure arriving as an HTTP status.
func TestResponses_RSP29_FailureRetryClasses(t *testing.T) {
	cases := []struct {
		name      string
		frame     string
		retryable bool
	}{
		{"rate limit", `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"slow down"}}}`, true},
		{"server error", `{"type":"response.failed","response":{"error":{"code":"server_error","message":"oops"}}}`, false},
		{"overload message", `{"type":"error","message":"The server is overloaded"}`, true},
		{"bad prompt", `{"type":"response.failed","response":{"error":{"code":"invalid_prompt","message":"no"}}}`, false},
	}
	for _, c := range cases {
		typ := "response.failed"
		if strings.Contains(c.frame, `"type":"error"`) {
			typ = "error"
		}
		p, _ := newProvider(t, Config{}, sse(typ, c.frame))
		_, _, err := run(t, p, provider.Request{Model: "m"})
		var perr *provider.Error
		if !errors.As(err, &perr) || perr.Retryable != c.retryable {
			t.Errorf("%s: err = %v, want retryable=%v", c.name, err, c.retryable)
		}
	}
}

func TestResponses_RSP13_TopLevelError(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("error", `{"type":"error","error":{"message":"bad"}}`),
	)
	_, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Retryable || perr.Message != "bad" {
		t.Fatalf("err = %v", err)
	}
}

func TestResponses_RSP14_UnknownEventsIgnored(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.created", `{"type":"response.created"}`),
		sse("foo", `{"type":"foo","delta":"x"}`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil || resp.Message.Content != "" {
		t.Fatalf("resp = %+v err = %v", resp, err)
	}
}

func TestResponses_RSP15_FinishToolCalls(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"i1","type":"function_call","call_id":"c1","name":"read"}}`),
		sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish = %q, want tool_calls", resp.FinishReason)
	}
}

func TestResponses_RSP16_EndsBeforeCompleted(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.output_text.delta", `{"type":"response.output_text.delta","delta":"partial"}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable {
		t.Fatalf("err = %v, want a retryable truncation", err)
	}
	if resp == nil || resp.Message.Content != "partial" {
		t.Fatalf("partial = %+v", resp)
	}
}

func TestResponses_RSP17_EmptyStream(t *testing.T) {
	p, _ := newProvider(t, Config{})
	_, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable || !strings.Contains(perr.Message, "empty stream") {
		t.Fatalf("err = %v, want a retryable empty stream", err)
	}
}

func TestResponses_RSP18_ConnectTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	p := New(Config{BaseURL: srv.URL, ConnectTimeout: 50 * time.Millisecond})
	_, _, err := run(t, p, provider.Request{Model: "m"})
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable {
		t.Fatalf("err = %v, want a retryable connect timeout", err)
	}
}

func TestResponses_RSP19_IdleTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, sse("response.output_text.delta", `{"type":"response.output_text.delta","delta":"hi"}`))
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
		t.Fatalf("err = %v, want a retryable idle timeout", err)
	}
	if resp == nil || resp.Message.Content != "hi" {
		t.Fatalf("partial = %+v", resp)
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// FuzzResponsesStream feeds arbitrary bytes as a Responses SSE body.
func FuzzResponsesStream(f *testing.F) {
	f.Add([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"))
	f.Add([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	f.Add([]byte("not sse at all"))
	f.Fuzz(func(t *testing.T, data []byte) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
		})}
		p := New(Config{BaseURL: "http://example.invalid", HTTPClient: client})
		_, _ = p.Stream(context.Background(), provider.Request{Model: "m"}, nil)
	})
}

func TestResponses_RSP20_ImageMessageContentArray(t *testing.T) {
	p, srv := newProvider(t, Config{}, sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`))
	_, _, err := run(t, p, provider.Request{Model: "m", Messages: []message.Message{
		message.UserParts("what is this?", message.ImagePart("image/png", "aGk=")),
	}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(srv.Requests()[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	input, _ := body["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input = %v, want 1 item", input)
	}
	content, ok := input[0].(map[string]any)["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("content = %v, want [input_text, input_image]", input[0].(map[string]any)["content"])
	}
	if content[0].(map[string]any)["type"] != "input_text" {
		t.Errorf("first block = %v, want input_text", content[0])
	}
	second, _ := content[1].(map[string]any)
	if second["type"] != "input_image" || second["image_url"] != "data:image/png;base64,aGk=" {
		t.Errorf("second block = %v, want an input_image data URL", second)
	}
}

func TestResponses_RSP21_TextOnlyContentStaysString(t *testing.T) {
	p, srv := newProvider(t, Config{}, sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`))
	_, _, err := run(t, p, provider.Request{Model: "m", Messages: []message.Message{{Role: message.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(srv.Requests()[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	got := body["input"].([]any)[0].(map[string]any)["content"]
	if got != "hi" {
		t.Errorf("content = %T %v, want the string \"hi\"", got, got)
	}
}

// --- prompt caching: RSP22 -------------------------------------------------

// cacheModel registers a catalog entry for the marker decision and returns its
// id. The catalog is process-wide, so every test uses its own id.
func cacheModel(t *testing.T, id string, implicit bool) string {
	t.Helper()
	provider.RegisterModel(provider.Model{ID: id, Provider: "test", Slug: id, ImplicitCaching: implicit})
	return id
}

// cacheMarkerOf returns the request body's top-level cache_control, or nil.
func cacheMarkerOf(t *testing.T, srv *testkit.Server) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(srv.Requests()[0].Body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	marker, _ := body["cache_control"].(map[string]any)
	return marker
}

func TestResponses_RSP22_CacheMarkerPerCatalog(t *testing.T) {
	implicit := cacheModel(t, "rsp22-implicit", true)
	explicit := cacheModel(t, "rsp22-explicit", false)
	cases := []struct {
		name  string
		model string
		want  bool
	}{
		{"non-implicit model", explicit, true},
		{"implicit model", implicit, false},
		{"unknown model", "rsp22-unknown", false},
	}
	for _, tc := range cases {
		p, srv := newProvider(t, Config{}, sse("response.completed", `{"type":"response.completed","response":{"usage":{}}}`))
		req := provider.Request{Model: tc.model, Messages: []message.Message{message.User("hi")}}
		if _, _, err := run(t, p, req); err != nil {
			t.Fatalf("%s: Stream: %v", tc.name, err)
		}
		marker := cacheMarkerOf(t, srv)
		if (marker != nil) != tc.want {
			t.Errorf("%s: cache_control = %v, want marker %v", tc.name, marker, tc.want)
			continue
		}
		if marker != nil && marker["type"] != "ephemeral" {
			t.Errorf("%s: marker type = %v, want ephemeral", tc.name, marker["type"])
		}
	}
}

// TestResponses_RSP23_CacheWriteTokensMapped checks the write half of the
// cache accounting on the Responses dialect.
func TestResponses_RSP23_CacheWriteTokensMapped(t *testing.T) {
	p, _ := newProvider(t, Config{},
		sse("response.completed", `{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"input_tokens_details":{"cached_tokens":3,"cache_write_tokens":7}}}}`),
	)
	resp, _, err := run(t, p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Usage.CachedTokens != 3 || resp.Usage.CacheWriteTokens != 7 {
		t.Fatalf("usage = %+v, want 3 read and 7 written", resp.Usage)
	}
}
