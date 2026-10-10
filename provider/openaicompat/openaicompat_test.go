package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// --- helpers ---------------------------------------------------------------

// sink records the events a test's Stream callback receives.
type sink struct {
	events []provider.StreamEvent
}

func (s *sink) emit(ev provider.StreamEvent) { s.events = append(s.events, ev) }

func (s *sink) count(kind provider.StreamEventType) int {
	n := 0
	for _, ev := range s.events {
		if ev.Type == kind {
			n++
		}
	}
	return n
}

func (s *sink) text() string {
	var b strings.Builder
	for _, ev := range s.events {
		if ev.Type == provider.StreamTextDelta {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

func (s *sink) reasoning() string {
	var b strings.Builder
	for _, ev := range s.events {
		if ev.Type == provider.StreamReasoningDelta {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

func (s *sink) calls() []message.ToolCall {
	var out []message.ToolCall
	for _, ev := range s.events {
		if ev.Type == provider.StreamToolCall && ev.ToolCall != nil {
			out = append(out, *ev.ToolCall)
		}
	}
	return out
}

func (s *sink) sawError() bool {
	for _, ev := range s.events {
		if ev.Type == provider.StreamError {
			return true
		}
	}
	return false
}

// served starts a testkit SSE server and points a provider at it.
func served(t *testing.T, cfg Config, turns ...testkit.ServerTurn) (*Provider, *testkit.Server) {
	t.Helper()
	srv := testkit.NewServer(t, turns...)
	cfg.BaseURL = srv.URL
	return New(cfg), srv
}

// bodyOf decodes the first request the server recorded.
func bodyOf(t *testing.T, srv *testkit.Server) map[string]any {
	t.Helper()
	reqs := srv.Requests()
	if len(reqs) == 0 {
		t.Fatal("server recorded no request")
	}
	var m map[string]any
	if err := json.Unmarshal(reqs[0].Body, &m); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, reqs[0].Body)
	}
	return m
}

// sseLine renders one SSE data event carrying v.
func sseLine(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return "data: " + string(b) + "\n\n"
}

// deltaLine renders one chunk with the given delta.
func deltaLine(d map[string]any) string {
	return sseLine(map[string]any{"choices": []any{map[string]any{"delta": d}}})
}

const doneLine = "data: [DONE]\n\n"

// assertErr requires a *provider.Error with the given status and retryability.
// A wantStatus of -1 skips the status check.
func assertErr(t *testing.T, err error, wantStatus int, wantRetryable bool) *provider.Error {
	t.Helper()
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v (%T), want *provider.Error", err, err)
	}
	if wantStatus >= 0 && pe.Status != wantStatus {
		t.Errorf("error status = %d, want %d (%v)", pe.Status, wantStatus, pe)
	}
	if pe.Retryable != wantRetryable {
		t.Errorf("error retryable = %v, want %v (%v)", pe.Retryable, wantRetryable, pe)
	}
	return pe
}

// noHeadersServer never sends response headers and holds the connection until
// the client gives up or the test releases it. A release channel is needed
// because net/http does not always notice a client close while a handler with
// an unread request body is blocked, which would wedge httptest's Close.
func noHeadersServer(t *testing.T, started chan<- struct{}) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if started != nil {
			close(started)
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv
}

// stallAfter writes first (when non-empty), flushes, then blocks until the
// client disconnects or the test releases it.
func stallAfter(t *testing.T, first string) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first != "" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, first)
			w.(http.Flusher).Flush()
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv
}

func textRequest(model string) provider.Request {
	return provider.Request{Model: model, Messages: []message.Message{message.User("hi")}}
}

// --- request shape: OAI01-OAI12 -------------------------------------------

func TestStream_OAI01_PlainTextTurn(t *testing.T) {
	usage := provider.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{
		TextChunks: []string{"He", "ll", "o"},
		Usage:      &usage,
	}})
	var s sink
	resp, err := p.Stream(context.Background(), textRequest("m"), s.emit)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := s.count(provider.StreamTextDelta); got != 3 {
		t.Errorf("text deltas = %d, want 3", got)
	}
	if resp.Message.Content != "Hello" {
		t.Errorf("content = %q, want Hello", resp.Message.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish = %q, want stop", resp.FinishReason)
	}
	if resp.Usage.PromptTokens != 3 || resp.Usage.TotalTokens != 8 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestStream_OAI02_RequestBodyShape(t *testing.T) {
	p, srv := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	if _, err := p.Stream(context.Background(), textRequest("m"), nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	m := bodyOf(t, srv)
	if m["stream"] != true {
		t.Errorf("stream = %v, want true", m["stream"])
	}
	opts, _ := m["stream_options"].(map[string]any)
	if opts["include_usage"] != true {
		t.Errorf("stream_options = %v, want include_usage true", m["stream_options"])
	}
	if m["model"] != "m" {
		t.Errorf("model = %v, want m", m["model"])
	}
	if msgs, _ := m["messages"].([]any); len(msgs) != 1 {
		t.Errorf("messages = %v, want 1", m["messages"])
	}
}

func TestStream_OAI03_ToolsAndToolChoiceSent(t *testing.T) {
	p, srv := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	req := textRequest("m")
	req.Tools = []provider.ToolDef{{Type: "function", Function: provider.ToolFunction{Name: "read"}}}
	req.ToolChoice = "auto"
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	m := bodyOf(t, srv)
	if _, ok := m["tools"]; !ok {
		t.Errorf("tools missing from body: %v", m)
	}
	if m["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", m["tool_choice"])
	}
}

func TestStream_OAI04_OptionalFieldsOmitted(t *testing.T) {
	p, srv := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	if _, err := p.Stream(context.Background(), textRequest("m"), nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	m := bodyOf(t, srv)
	for _, key := range []string{"temperature", "max_tokens", "stop", "reasoning_effort"} {
		if _, ok := m[key]; ok {
			t.Errorf("%s should be absent, body has %v", key, m[key])
		}
	}
}

func TestStream_OAI05_StopAndReasoningEffortSent(t *testing.T) {
	p, srv := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	req := textRequest("m")
	req.Stop = []string{"END"}
	req.ReasoningEffort = "high"
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	m := bodyOf(t, srv)
	if stops, _ := m["stop"].([]any); len(stops) != 1 || stops[0] != "END" {
		t.Errorf("stop = %v, want [END]", m["stop"])
	}
	if m["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", m["reasoning_effort"])
	}
}

func TestStream_OAI06_ProviderOptionsMergedTopLevel(t *testing.T) {
	p, srv := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	req := textRequest("m")
	req.ProviderOptions = map[string]any{"provider": map[string]any{"order": []any{"a"}}}
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	m := bodyOf(t, srv)
	got, _ := m["provider"].(map[string]any)
	if got == nil {
		t.Fatalf("provider missing: %v", m)
	}
	order, _ := got["order"].([]any)
	if len(order) != 1 || order[0] != "a" {
		t.Errorf("provider.order = %v, want [a]", got["order"])
	}
}

func TestStream_OAI07_ProviderOptionsNestedMerge(t *testing.T) {
	p, srv := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	req := textRequest("m")
	req.ProviderOptions = map[string]any{
		"provider": map[string]any{"order": []any{"a"}, "extra": float64(1)},
	}
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	m := bodyOf(t, srv)
	got, _ := m["provider"].(map[string]any)
	if got["extra"] != float64(1) {
		t.Errorf("provider.extra = %v, want 1", got["extra"])
	}
	if m["model"] != "m" || m["stream"] != true {
		t.Errorf("existing fields were not preserved: %v", m)
	}
}

func TestStream_OAI08_ReasoningNotResentByDefault(t *testing.T) {
	p, srv := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	req := provider.Request{Model: "m", Messages: []message.Message{{
		Role:      message.RoleAssistant,
		Content:   "hi",
		Reasoning: "secret thinking",
	}}}
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	m := bodyOf(t, srv)
	msgs, _ := m["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if _, ok := first["reasoning"]; ok {
		t.Errorf("reasoning re-sent by default: %v", first)
	}
}

func TestStream_OAI09_HeadersPrecedence(t *testing.T) {
	p, srv := served(t, Config{Headers: map[string]string{"X-Test": "1"}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	req := textRequest("m")
	req.ExtraHeaders = map[string]string{"X-Test": "2"}
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := srv.Requests()[0].Header.Get("X-Test"); got != "2" {
		t.Errorf("X-Test = %q, want 2", got)
	}
}

func TestStream_OAI10_AuthorizationFromAPIKeyFunc(t *testing.T) {
	p, srv := served(t, Config{
		APIKey:     "k1",
		APIKeyFunc: func(context.Context) (string, error) { return "k2", nil },
	}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	if _, err := p.Stream(context.Background(), textRequest("m"), nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := srv.Requests()[0].Header.Get("Authorization"); got != "Bearer k2" {
		t.Errorf("Authorization = %q, want Bearer k2", got)
	}
}

func TestStream_OAI11_APIKeyFuncError(t *testing.T) {
	wantErr := errors.New("no credentials")
	p, srv := served(t, Config{
		APIKeyFunc: func(context.Context) (string, error) { return "", wantErr },
	}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	_, err := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, err, -1, false)
	if !errors.Is(pe, wantErr) {
		t.Errorf("error does not wrap the cause: %v", pe)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("server saw %d requests, want 0", n)
	}
}

func TestStream_OAI12_ContextAlreadyCancelled(t *testing.T) {
	p, srv := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Stream(ctx, textRequest("m"), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("server saw %d requests, want 0", n)
	}
}

// --- HTTP and connect failures: OAI13-OAI20 -------------------------------

func TestStream_OAI13_Unauthorized(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}, Fault: testkit.Fault401})
	_, err := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, err, http.StatusUnauthorized, false)
	if !strings.Contains(pe.Message, "invalid api key") {
		t.Errorf("message = %q, want the body's error text", pe.Message)
	}
}

func TestStream_OAI14_429RetryAfter(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}, Fault: testkit.Fault429})
	_, err := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, err, http.StatusTooManyRequests, true)
	if pe.RetryAfter != time.Second {
		t.Errorf("RetryAfter = %v, want 1s", pe.RetryAfter)
	}
}

func TestStream_OAI15_ServerError(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}, Fault: testkit.Fault500})
	_, err := p.Stream(context.Background(), textRequest("m"), nil)
	assertErr(t, err, http.StatusInternalServerError, true)
}

func TestStream_OAI16_HTMLBody(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}, Fault: testkit.FaultHTML})
	_, err := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, err, http.StatusBadGateway, true)
	if strings.ContainsAny(pe.Message, "<>") {
		t.Errorf("message still has tags: %q", pe.Message)
	}
	if !strings.Contains(pe.Message, "bad gateway") {
		t.Errorf("message = %q, want the tag-stripped text", pe.Message)
	}
	if len(pe.Message) > 500 {
		t.Errorf("message is %d bytes, want at most 500", len(pe.Message))
	}
}

func TestStream_OAI17_StreamErrorObjectOverloaded(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		sseLine(map[string]any{"error": map[string]any{"message": "the server is overloaded"}}),
	}})
	_, err := p.Stream(context.Background(), textRequest("m"), nil)
	assertErr(t, err, http.StatusOK, true)
}

func TestStream_OAI18_StreamErrorObjectOtherCode(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		sseLine(map[string]any{"error": map[string]any{"code": "invalid_api_key", "message": "bad key"}}),
	}})
	pe := assertErr(t, streamErr(t, p), http.StatusOK, false)
	if pe.Code != "invalid_api_key" {
		t.Errorf("code = %q, want invalid_api_key", pe.Code)
	}
}

// streamErr runs one stream and returns its error, for table rows that only
// care about the failure.
func streamErr(t *testing.T, p *Provider) error {
	t.Helper()
	_, err := p.Stream(context.Background(), textRequest("m"), nil)
	return err
}

func TestStream_OAI19_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	p := New(Config{BaseURL: url})
	_, e := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, e, -1, true)
	if pe.Err == nil {
		t.Errorf("connection error should carry the cause: %v", pe)
	}
}

func TestStream_OAI20_CancelDuringConnect(t *testing.T) {
	started := make(chan struct{})
	srv := noHeadersServer(t, started)
	p := New(Config{BaseURL: srv.URL})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Stream(ctx, textRequest("m"), nil)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// --- stream lifecycle: OAI21-OAI27 ----------------------------------------

func TestStream_OAI21_CancelMidStream(t *testing.T) {
	srv := stallAfter(t, deltaLine(map[string]any{"content": "hi"}))
	p := New(Config{BaseURL: srv.URL})

	ctx, cancel := context.WithCancel(context.Background())
	var s sink
	resp, err := p.Stream(ctx, textRequest("m"), func(ev provider.StreamEvent) {
		s.emit(ev)
		if ev.Type == provider.StreamTextDelta {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if s.text() != "hi" {
		t.Errorf("delivered text = %q, want hi", s.text())
	}
	if resp == nil || resp.Message.Content != "hi" {
		t.Errorf("partial response = %+v, want content hi", resp)
	}
}

func TestStream_OAI22_CancelAfterLastChunk(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}})
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := p.Stream(ctx, textRequest("m"), func(ev provider.StreamEvent) {
		if ev.Type == provider.StreamTextDelta {
			cancel()
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want nil or context.Canceled", err)
	}
	if resp == nil || resp.Message.Content != "hi" {
		t.Errorf("response = %+v, want content hi", resp)
	}
}

func TestStream_OAI23_EmptyStream(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{}})
	_, e := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, e, -1, true)
	if !strings.Contains(pe.Message, "empty stream") {
		t.Errorf("message = %q, want empty stream", pe.Message)
	}
}

func TestStream_OAI24_NoDoneButFinishSeen(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.FaultNoDone})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "hi" || resp.FinishReason != "stop" {
		t.Errorf("response = %+v, want hi/stop", resp)
	}
}

func TestStream_OAI25_NoDoneNoFinish(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		deltaLine(map[string]any{"content": "hi"}),
	}})
	resp, e := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, e, -1, true)
	if !strings.Contains(pe.Message, "before completion") {
		t.Errorf("message = %q, want stream ended before completion", pe.Message)
	}
	if resp == nil || resp.Message.Content != "hi" {
		t.Errorf("partial response = %+v, want content hi", resp)
	}
}

func TestStream_OAI26_CutMidEvent(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.FaultCutMidEvent})
	resp, e := p.Stream(context.Background(), textRequest("m"), nil)
	assertErr(t, e, -1, true)
	if resp != nil && len(resp.Message.ToolCalls) != 0 {
		t.Errorf("partial tool calls were emitted: %+v", resp.Message.ToolCalls)
	}
}

func TestStream_OAI27_MalformedJSONNamesLine(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		deltaLine(map[string]any{"content": "hi"}),
		"data: {not valid json}\n\n",
	}})
	resp, e := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, e, -1, false)
	if !strings.Contains(pe.Message, "line 3") {
		t.Errorf("message = %q, want it to name the malformed physical line 3", pe.Message)
	}
	if resp == nil || resp.Message.Content != "hi" {
		t.Errorf("partial response = %+v, want content hi", resp)
	}
}

// --- line handling: OAI28-OAI31 -------------------------------------------

func TestStream_OAI28_CommentAndBlankLines(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		": ping\n\n",
		deltaLine(map[string]any{"content": "hi"}),
		": ping\n",
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "hi" {
		t.Errorf("content = %q, want hi", resp.Message.Content)
	}
}

func TestStream_OAI29_DataWithoutSpace(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		`data:{"choices":[{"delta":{"content":"hi"}}]}` + "\n\n",
		"data:[DONE]\n\n",
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "hi" {
		t.Errorf("content = %q, want hi", resp.Message.Content)
	}
}

func TestStream_OAI30_CRLFLineEndings(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		"data: " + `{"choices":[{"delta":{"content":"hi"}}]}` + "\r\n\r\n",
		"data: [DONE]\r\n\r\n",
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "hi" {
		t.Errorf("content = %q, want hi", resp.Message.Content)
	}
}

func TestStream_OAI31_EventAndIDLinesIgnored(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		"event: message\n",
		"id: 42\n",
		"retry: 100\n",
		deltaLine(map[string]any{"content": "hi"}),
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "hi" {
		t.Errorf("content = %q, want hi", resp.Message.Content)
	}
}

// --- chunk shapes: OAI32-OAI37 --------------------------------------------

func TestStream_OAI32_UsageOnlyFinalChunk(t *testing.T) {
	usage := provider.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi", Usage: &usage}})
	var s sink
	resp, err := p.Stream(context.Background(), textRequest("m"), s.emit)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Usage.PromptTokens != 1 || resp.Usage.TotalTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if s.count(provider.StreamUsage) != 1 {
		t.Errorf("usage events = %d, want 1", s.count(provider.StreamUsage))
	}
}

func TestStream_OAI33_MissingChoicesAndNullDelta(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		sseLine(map[string]any{"usage": map[string]any{"prompt_tokens": 1}}),
		`data: {"choices":[{"delta":null}]}` + "\n\n",
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "" {
		t.Errorf("content = %q, want empty", resp.Message.Content)
	}
}

func TestStream_OAI34_NullContent(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		`data: {"choices":[{"delta":{"content":null}}]}` + "\n\n",
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "" {
		t.Errorf("content = %q, want empty", resp.Message.Content)
	}
}

func TestStream_OAI35_ReasoningContent(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi", Reasoning: "think"}})
	var s sink
	resp, err := p.Stream(context.Background(), textRequest("m"), s.emit)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if s.reasoning() != "think" || resp.Message.Reasoning != "think" {
		t.Errorf("reasoning = %q / %q, want think", s.reasoning(), resp.Message.Reasoning)
	}
}

func TestStream_OAI36_ReasoningField(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		deltaLine(map[string]any{"reasoning": "think"}),
		doneLine,
	}})
	var s sink
	resp, err := p.Stream(context.Background(), textRequest("m"), s.emit)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if s.reasoning() != "think" || resp.Message.Reasoning != "think" {
		t.Errorf("reasoning = %q / %q, want think", s.reasoning(), resp.Message.Reasoning)
	}
}

func TestStream_OAI37_ReasoningDetails(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		deltaLine(map[string]any{"reasoning_details": []any{
			map[string]any{"text": "a"}, map[string]any{"text": "b"},
		}}),
		doneLine,
	}})
	var s sink
	resp, err := p.Stream(context.Background(), textRequest("m"), s.emit)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if s.reasoning() != "ab" || resp.Message.Reasoning != "ab" {
		t.Errorf("reasoning = %q / %q, want ab", s.reasoning(), resp.Message.Reasoning)
	}
}

// --- tool-call assembly: OAI38-OAI47 --------------------------------------

func TestStream_OAI38_SingleToolCallThreeFragments(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{
		ToolCalls: []testkit.Call{{ID: "c1", Name: "echo", SplitArgs: []string{`{"x":`, "1", `}`}}},
		Finish:    "tool_calls",
	}})
	var s sink
	resp, err := p.Stream(context.Background(), textRequest("m"), s.emit)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	calls := s.calls()
	if len(calls) != 1 {
		t.Fatalf("emitted calls = %d, want 1", len(calls))
	}
	if calls[0].Function.Arguments != `{"x":1}` {
		t.Errorf("arguments = %q, want {\"x\":1}", calls[0].Function.Arguments)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Errorf("response calls = %d, want 1", len(resp.Message.ToolCalls))
	}
}

func TestStream_OAI39_TwoToolCallsInterleaved(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		deltaLine(map[string]any{"tool_calls": []any{tc(0, "c0", "a", `{"x":`)}}),
		deltaLine(map[string]any{"tool_calls": []any{tc(1, "c1", "b", `{"y":`)}}),
		deltaLine(map[string]any{"tool_calls": []any{tc(0, "", "", "1}")}}),
		deltaLine(map[string]any{"tool_calls": []any{tc(1, "", "", "2}")}}),
		finishLine("tool_calls"),
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	calls := resp.Message.ToolCalls
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].ID != "c0" || calls[0].Function.Arguments != `{"x":1}` {
		t.Errorf("call 0 = %+v", calls[0])
	}
	if calls[1].ID != "c1" || calls[1].Function.Arguments != `{"y":2}` {
		t.Errorf("call 1 = %+v", calls[1])
	}
}

func TestStream_OAI40_ToolCallWithoutID(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{
		ToolCalls: []testkit.Call{{Name: "a", Args: "{}"}, {Name: "b", Args: "{}"}},
		Finish:    "tool_calls",
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	ids := map[string]bool{}
	for i, c := range resp.Message.ToolCalls {
		if !strings.HasPrefix(c.ID, fmt.Sprintf("call_%d_", i)) {
			t.Errorf("call %d id = %q, want call_%d_<n>", i, c.ID, i)
		}
		if ids[c.ID] {
			t.Errorf("duplicate id %q", c.ID)
		}
		ids[c.ID] = true
	}
}

func TestStream_OAI41_ToolCallIDArrivesLate(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		deltaLine(map[string]any{"tool_calls": []any{tc(0, "", "a", "{}")}}),
		deltaLine(map[string]any{"tool_calls": []any{tc(0, "late", "", "")}}),
		finishLine("tool_calls"),
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "late" {
		t.Errorf("calls = %+v, want id late", resp.Message.ToolCalls)
	}
}

func TestStream_OAI42_EmptyArgumentsBecomeObject(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{
		ToolCalls: []testkit.Call{{ID: "c1", Name: "a"}},
		Finish:    "tool_calls",
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.ToolCalls[0].Function.Arguments != "{}" {
		t.Errorf("arguments = %q, want {}", resp.Message.ToolCalls[0].Function.Arguments)
	}
}

func TestStream_OAI43_InvalidJSONArgumentsKept(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{
		ToolCalls: []testkit.Call{{ID: "c1", Name: "a", Args: `{"x":`}},
		Finish:    "tool_calls",
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := resp.Message.ToolCalls[0].Function.Arguments; got != `{"x":` {
		t.Errorf("arguments = %q, want the original invalid JSON", got)
	}
}

func TestStream_OAI44_EmptyToolCallNameDropped(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{
		ToolCalls: []testkit.Call{{ID: "c1", Name: "", Args: "{}"}},
		Finish:    "tool_calls",
	}})
	var s sink
	resp, err := p.Stream(context.Background(), textRequest("m"), s.emit)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(resp.Message.ToolCalls) != 0 {
		t.Errorf("nameless call kept: %+v", resp.Message.ToolCalls)
	}
	if !s.sawError() {
		t.Errorf("expected a StreamError event, got %+v", s.events)
	}
}

func TestStream_OAI45_TextAndToolCalls(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{
		Text:      "hi",
		ToolCalls: []testkit.Call{{ID: "c1", Name: "a", Args: "{}"}},
		Finish:    "tool_calls",
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "hi" || len(resp.Message.ToolCalls) != 1 {
		t.Errorf("response = %+v", resp.Message)
	}
}

func TestStream_OAI46_FinishStopWithToolCalls(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{
		Text:      "hi",
		ToolCalls: []testkit.Call{{ID: "c1", Name: "a", Args: "{}"}},
		Finish:    "stop",
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("finish = %q, want tool_calls", resp.FinishReason)
	}
}

func TestStream_OAI47_FinishReasonAliases(t *testing.T) {
	cases := map[string]string{"end_turn": "stop", "tool_use": "tool_calls", "max_tokens": "length"}
	for alias, want := range cases {
		t.Run(alias, func(t *testing.T) {
			p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi", Finish: alias}})
			resp, err := p.Stream(context.Background(), textRequest("m"), nil)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if resp.FinishReason != want {
				t.Errorf("finish = %q, want %q", resp.FinishReason, want)
			}
		})
	}
}

// --- finish, usage, model: OAI48-OAI52 ------------------------------------

func TestStream_OAI48_FinishReasonMissing(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		deltaLine(map[string]any{"content": "hi"}),
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.FinishReason != "" {
		t.Errorf("finish = %q, want empty", resp.FinishReason)
	}
}

func TestStream_OAI49_UsageMapping(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		sseLine(map[string]any{"choices": []any{}, "usage": map[string]any{
			"prompt_tokens":             10,
			"completion_tokens":         20,
			"total_tokens":              30,
			"cost":                      0.002,
			"prompt_tokens_details":     map[string]any{"cached_tokens": 4},
			"completion_tokens_details": map[string]any{"reasoning_tokens": 7},
		}}),
		doneLine,
	}})
	var s sink
	resp, err := p.Stream(context.Background(), textRequest("m"), s.emit)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	u := resp.Usage
	if u.PromptTokens != 10 || u.CompletionTokens != 20 || u.TotalTokens != 30 {
		t.Errorf("tokens = %+v", u)
	}
	if u.CachedTokens != 4 || u.ReasoningTokens != 7 {
		t.Errorf("details = %+v", u)
	}
	if u.CostUSD != 0.002 || u.CostSource != "provider" {
		t.Errorf("cost = %+v", u)
	}
	if s.count(provider.StreamUsage) != 1 {
		t.Errorf("usage events = %d, want 1", s.count(provider.StreamUsage))
	}
}

func TestStream_OAI50_UsageAbsent(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Usage != (provider.Usage{}) {
		t.Errorf("usage = %+v, want zero", resp.Usage)
	}
}

func TestStream_OAI51_ModelFromStream(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		sseLine(map[string]any{"model": "m-1", "choices": []any{map[string]any{"delta": map[string]any{"content": "hi"}}}}),
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Model != "m-1" {
		t.Errorf("model = %q, want m-1", resp.Model)
	}
}

func TestStream_OAI52_NilCallback(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		deltaLine(map[string]any{"content": "hi"}),
		finishLine("stop"),
		doneLine,
	}})
	if _, err := p.Stream(context.Background(), textRequest("m"), nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
}

// --- robustness: OAI53-OAI60 ----------------------------------------------

func TestStream_OAI53_VeryLargeChunk(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.FaultHugeChunk})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(resp.Message.Content) != 4<<20 {
		t.Errorf("content length = %d, want %d", len(resp.Message.Content), 4<<20)
	}
}

func TestStream_OAI54_LineOverMaxLineBytes(t *testing.T) {
	p, _ := served(t, Config{MaxLineBytes: 1 << 10},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.FaultHugeChunk})
	_, e := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, e, -1, false)
	if !strings.Contains(pe.Message, "1024") {
		t.Errorf("message = %q, want it to name the 1024 byte limit", pe.Message)
	}
}

func TestStream_OAI55_ManyTinyChunks(t *testing.T) {
	chunks := make([]string, 5000)
	for i := range chunks {
		chunks[i] = "x"
	}
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{TextChunks: chunks}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(resp.Message.Content) != 5000 {
		t.Errorf("content length = %d, want 5000", len(resp.Message.Content))
	}
}

func TestStream_OAI56_UnicodeSplitAcrossReads(t *testing.T) {
	// The euro sign is three bytes; split them across two network writes.
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		"data: " + `{"choices":[{"delta":{"content":"` + "\xe2\x82",
		"\xac" + `"}}]}` + "\n\n",
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "€" {
		t.Errorf("content = %q, want €", resp.Message.Content)
	}
}

func TestStream_OAI57_IdleTimeout(t *testing.T) {
	srv := stallAfter(t, deltaLine(map[string]any{"content": "hi"}))
	p := New(Config{BaseURL: srv.URL, IdleTimeout: 50 * time.Millisecond})
	_, e := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, e, -1, true)
	if !strings.Contains(strings.ToLower(pe.Message), "idle") {
		t.Errorf("message = %q, want an idle timeout", pe.Message)
	}
}

func TestStream_OAI58_ConnectTimeout(t *testing.T) {
	srv := noHeadersServer(t, nil)
	p := New(Config{BaseURL: srv.URL, ConnectTimeout: 50 * time.Millisecond})
	_, e := p.Stream(context.Background(), textRequest("m"), nil)
	pe := assertErr(t, e, -1, true)
	if !strings.Contains(strings.ToLower(pe.Message), "connect") {
		t.Errorf("message = %q, want a connect timeout", pe.Message)
	}
}

func TestStream_OAI59_NoGoroutineOrConnectionLeak(t *testing.T) {
	testkit.NoLeaks(t)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	stall := stallAfter(t, deltaLine(map[string]any{"content": "hi"}))
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	refusedURL := refused.URL
	refused.Close()

	cases := []struct {
		name string
		cfg  Config
		turn testkit.ServerTurn
	}{
		{"429", Config{HTTPClient: client}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.Fault429}},
		{"500", Config{HTTPClient: client}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.Fault500}},
		{"html", Config{HTTPClient: client}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.FaultHTML}},
		{"malformed", Config{HTTPClient: client}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.FaultMalformedJSON}},
		{"cut", Config{HTTPClient: client}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.FaultCutMidEvent}},
		{"huge", Config{HTTPClient: client}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}, Fault: testkit.FaultHugeChunk}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := served(t, tc.cfg, tc.turn)
			_, _ = p.Stream(context.Background(), textRequest("m"), nil)
		})
	}
	t.Run("refused", func(t *testing.T) {
		p := New(Config{BaseURL: refusedURL, HTTPClient: client})
		_, _ = p.Stream(context.Background(), textRequest("m"), nil)
	})
	t.Run("idle", func(t *testing.T) {
		p := New(Config{BaseURL: stall.URL, HTTPClient: client, IdleTimeout: 50 * time.Millisecond})
		_, _ = p.Stream(context.Background(), textRequest("m"), nil)
	})
}

func TestStream_OAI60_UnknownFieldsIgnored(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{
		`data: {"unknown":1,"choices":[{"unknown":2,"delta":{"unknown":3,"content":"hi"}}],"model":"m","usage":null}` + "\n\n",
		doneLine,
	}})
	resp, err := p.Stream(context.Background(), textRequest("m"), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message.Content != "hi" {
		t.Errorf("content = %q, want hi", resp.Message.Content)
	}
}

// --- request immutability and identity: OAI61-OAI62 -----------------------

func TestStream_OAI61_RequestNotMutated(t *testing.T) {
	p, _ := served(t, Config{}, testkit.ServerTurn{Turn: testkit.Turn{Text: "hi"}})
	temp := 0.5
	req := textRequest("m")
	req.Temperature = &temp
	req.Stop = []string{"END"}
	req.ProviderOptions = map[string]any{"provider": map[string]any{"order": []any{"a"}}}
	req.ExtraHeaders = map[string]string{"X-Test": "1"}
	before, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal before: %v", err)
	}
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	after, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("request mutated:\n before %s\n after  %s", before, after)
	}
}

func TestStream_OAI62_NameAndBaseURL(t *testing.T) {
	p := New(Config{Name: "or", BaseURL: "https://example.test/api/v1/"})
	if p.Name() != "or" {
		t.Errorf("Name = %q, want or", p.Name())
	}
	if p.BaseURL() != "https://example.test/api/v1" {
		t.Errorf("BaseURL = %q, want the trailing slash trimmed", p.BaseURL())
	}
	if New(Config{}).Name() != "openaicompat" {
		t.Errorf("default Name = %q, want openaicompat", New(Config{}).Name())
	}
}

// --- helpers for chunk construction ---------------------------------------

// tc builds one tool-call delta object. Empty fields are omitted so a test can
// send only an argument fragment or only a late id.
func tc(index int, id, name, args string) map[string]any {
	fn := map[string]any{}
	if name != "" {
		fn["name"] = name
	}
	if args != "" {
		fn["arguments"] = args
	}
	out := map[string]any{"index": index, "function": fn}
	if id != "" {
		out["id"] = id
		out["type"] = "function"
	}
	return out
}

// finishLine renders a chunk that carries only a finish reason.
func finishLine(reason string) string {
	return sseLine(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": reason}}})
}

// --- keep the prototype's smoke tests -------------------------------------

func TestStreamParsesTextAndToolCalls(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth header, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer is not a flusher")
			return
		}
		write := func(s string) {
			fmt.Fprint(w, s)
			flusher.Flush()
		}
		write(deltaLine(map[string]any{"content": "Hel"}))
		write(deltaLine(map[string]any{"content": "lo"}))
		write(deltaLine(map[string]any{"tool_calls": []any{tc(0, "call_1", "echo", `{"text":`)}}))
		write(deltaLine(map[string]any{"tool_calls": []any{tc(0, "", "", `"hi"}`)}}))
		write(finishLine("tool_calls"))
		write(doneLine)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "test-key"})

	var text string
	var sawToolCall bool
	resp, err := p.Stream(context.Background(), textRequest("test/model"), func(ev provider.StreamEvent) {
		switch ev.Type {
		case provider.StreamTextDelta:
			text += ev.Text
		case provider.StreamToolCall:
			sawToolCall = true
		}
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if text != "Hello" || resp.Message.Content != "Hello" {
		t.Fatalf("content = %q / %q, want Hello", text, resp.Message.Content)
	}
	if !sawToolCall || len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.Message.ToolCalls)
	}
	if got := resp.Message.ToolCalls[0].Function.Arguments; got != `{"text":"hi"}` {
		t.Fatalf("arguments = %q", got)
	}
}

func TestStreamReturnsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad model", http.StatusBadRequest)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL})
	if _, err := p.Stream(context.Background(), textRequest("x"), nil); err == nil {
		t.Fatal("expected an error for HTTP 400")
	}
}

// --- fuzz targets ----------------------------------------------------------

func FuzzStream(f *testing.F) {
	f.Add([]byte("data: [DONE]\n\n"))
	f.Add([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
	f.Add([]byte("data: {not json}\n\n"))
	f.Add([]byte("data:{\"choices\":[{\"delta\":{\"content\":\"a\"}}]}"))
	f.Fuzz(func(t *testing.T, data []byte) {
		p, _ := served(t, Config{}, testkit.ServerTurn{Raw: []string{string(data)}})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = p.Stream(ctx, textRequest("m"), func(provider.StreamEvent) {})
	})
}

func FuzzToolCallAssembly(f *testing.F) {
	f.Add(int64(0), uint8(0), uint8(1), uint8(2))
	f.Add(int64(1), uint8(3), uint8(1), uint8(3))
	f.Fuzz(func(t *testing.T, seed int64, a, b, c uint8) {
		const arguments = `{"x":1}`
		fragments := interleave(arguments, []int{int(a)%4 + 1, int(b)%4 + 1, int(c)%4 + 1})
		asm := &assembler{calls: map[int]*callAcc{}}
		for n, frag := range fragments {
			id := ""
			name := ""
			if n == 0 {
				id, name = "c1", "read"
			}
			asm.add(toolCallDelta{
				Index:    intPtr(0),
				ID:       id,
				Type:     "function",
				Function: funcDelta(name, frag),
			})
		}
		calls, dropped := asm.assembled()
		if dropped != 0 || len(calls) != 1 {
			t.Fatalf("seed %d: calls=%v dropped=%d", seed, calls, dropped)
		}
		if calls[0].ID != "c1" || calls[0].Function.Name != "read" {
			t.Fatalf("seed %d: call = %+v", seed, calls[0])
		}
		if calls[0].Function.Arguments != arguments {
			t.Fatalf("seed %d: arguments = %q, want %q", seed, calls[0].Function.Arguments, arguments)
		}
	})
}

// interleave splits s into len(parts) pieces of the requested sizes and returns
// them in order, cycling the sizes when there are more parts than sizes.
func interleave(s string, sizes []int) []string {
	var out []string
	i := 0
	for n := 0; i < len(s); n++ {
		size := sizes[n%len(sizes)]
		if size > len(s)-i {
			size = len(s) - i
		}
		out = append(out, s[i:i+size])
		i += size
	}
	return out
}

func intPtr(v int) *int { return &v }

// funcDelta fills the anonymous function field of a tool-call delta.
func funcDelta(name, args string) struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
} {
	return struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{Name: name, Arguments: args}
}
