package testkit

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
)

// Fault selects how the SSE server misbehaves for one turn.
type Fault int

const (
	// FaultNone serves the turn normally.
	FaultNone Fault = iota
	// FaultCutMidEvent closes the connection in the middle of an SSE event.
	FaultCutMidEvent
	// FaultNoDone ends the stream cleanly without a [DONE] sentinel.
	FaultNoDone
	// FaultMalformedJSON sends a data line that is not JSON.
	FaultMalformedJSON
	// FaultStall sends headers, then nothing until the client gives up.
	FaultStall
	// Fault401 responds 401 with a JSON error body.
	Fault401
	// Fault429 responds 429 with Retry-After: 1.
	Fault429
	// Fault500 responds 500 with a JSON error body.
	Fault500
	// FaultHTML responds 502 with an HTML body.
	FaultHTML
	// FaultHugeChunk sends one data line of at least 4 MiB.
	FaultHugeChunk
)

// ServerTurn is one scripted HTTP turn plus the fault to inject.
type ServerTurn struct {
	Turn
	Fault Fault
	// Raw, when non-nil, replaces the scripted turn: each element is written
	// verbatim to an SSE response body, in order, and flushed. An empty
	// non-nil slice therefore produces a 200 response with no body. Raw
	// bypasses every fault except none; it exists so tests can script wire
	// shapes the high-level Turn cannot express (comments, CRLF, missing
	// choices, late tool-call ids, usage detail objects).
	Raw []string
}

// knownStreamPath reports whether a POST path is one of the streaming
// endpoints the scripted server can play: chat completions, Responses, or
// Anthropic Messages.
func knownStreamPath(path string) bool {
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// RecordedRequest is what the server saw.
type RecordedRequest struct {
	Method, Path string
	Header       http.Header
	Body         []byte
}

// Server is a local HTTP server that speaks OpenAI chat-completions SSE.
type Server struct {
	URL string

	ts *httptest.Server

	mu    sync.Mutex
	turns []ServerTurn
	next  int
	reqs  []RecordedRequest
}

// NewServer starts a server that plays the turns in order. It is closed when
// the test's cleanup runs.
func NewServer(t testing.TB, turns ...ServerTurn) *Server {
	t.Helper()
	s := &Server{turns: turns}
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.ts.URL
	t.Cleanup(s.ts.Close)
	return s
}

// Requests returns the recorded requests, copied so callers cannot mutate them.
func (s *Server) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RecordedRequest, len(s.reqs))
	for i, r := range s.reqs {
		out[i] = RecordedRequest{
			Method: r.Method,
			Path:   r.Path,
			Header: r.Header.Clone(),
			Body:   append([]byte(nil), r.Body...),
		}
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !knownStreamPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, RecordedRequest{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	have := s.next < len(s.turns)
	var turn ServerTurn
	if have {
		turn = s.turns[s.next]
		s.next++
	}
	s.mu.Unlock()

	if !have {
		http.Error(w, `{"error":{"message":"testkit: script exhausted"}}`, http.StatusInternalServerError)
		return
	}

	if turn.Raw != nil {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, chunk := range turn.Raw {
			io.WriteString(w, chunk)
			flush(w)
		}
		return
	}

	switch turn.Fault {
	case Fault401:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
		return
	case Fault429:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limited"}}`)
		return
	case Fault500:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":{"message":"server error"}}`)
		return
	case FaultHTML:
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, `<html><body>bad gateway</body></html>`)
		return
	case FaultStall:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flush(w)
		<-r.Context().Done()
		return
	case FaultCutMidEvent:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"partial`)
		flush(w)
		return
	case FaultMalformedJSON:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {not valid json}\n\n")
		flush(w)
		return
	case FaultHugeChunk:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeEvent(w, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": strings.Repeat("x", 4<<20)}}},
		})
		io.WriteString(w, "data: [DONE]\n\n")
		flush(w)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	s.writeTurn(w, turn.Turn)
	if turn.Fault != FaultNoDone {
		io.WriteString(w, "data: [DONE]\n\n")
	}
	flush(w)
}

// writeTurn streams reasoning, text, tool calls, then usage, mirroring the
// in-process provider's order.
func (s *Server) writeTurn(w http.ResponseWriter, turn Turn) {
	first := true
	if turn.Reasoning != "" {
		s.writeDelta(w, roleOf(first), sseDelta{Reasoning: turn.Reasoning})
		first = false
	}
	chunks := turn.TextChunks
	if len(chunks) == 0 && turn.Text != "" {
		chunks = []string{turn.Text}
	}
	for _, text := range chunks {
		s.writeDelta(w, roleOf(first), sseDelta{Content: text})
		first = false
	}
	for i, call := range turn.ToolCalls {
		fragments := call.SplitArgs
		if len(fragments) == 0 {
			fragments = []string{call.args()}
		}
		s.writeDelta(w, roleOf(first), sseDelta{ToolCalls: []sseToolCall{{
			Index: i, ID: call.ID, Type: "function", Function: sseFunction{Name: call.Name},
		}}})
		first = false
		for _, frag := range fragments {
			s.writeDelta(w, "", sseDelta{ToolCalls: []sseToolCall{{
				Index: i, Function: sseFunction{Arguments: frag},
			}}})
		}
	}
	finish := turn.Finish
	if finish == "" {
		if len(turn.ToolCalls) > 0 {
			finish = "tool_calls"
		} else {
			finish = "stop"
		}
	}
	s.finish(w, finish)
	if turn.Usage != nil {
		writeEvent(w, sseChunk{ID: "chatcmpl-test", Object: "chat.completion.chunk", Model: "testkit", Choices: []sseChoice{}, Usage: usageWire(turn.Usage)})
	}
}

// finish writes a chunk that carries only a finish reason, the shape OpenAI
// uses to close a choice.
func (s *Server) finish(w http.ResponseWriter, reason string) {
	writeEvent(w, map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"model":   "testkit",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": reason}},
	})
}

func (s *Server) writeDelta(w http.ResponseWriter, role string, delta sseDelta) {
	if role != "" {
		delta.Role = role
	}
	writeEvent(w, sseChunk{
		ID:      "chatcmpl-test",
		Object:  "chat.completion.chunk",
		Model:   "testkit",
		Choices: []sseChoice{{Index: 0, Delta: delta}},
	})
}

func roleOf(first bool) string {
	if first {
		return "assistant"
	}
	return ""
}

// sseChunk is the wire shape of one chunk.
type sseChunk struct {
	ID      string      `json:"id,omitempty"`
	Object  string      `json:"object,omitempty"`
	Model   string      `json:"model,omitempty"`
	Choices []sseChoice `json:"choices"`
	Usage   *sseUsage   `json:"usage,omitempty"`
}

// sseUsage mirrors the OpenAI usage object, including the detail sub-objects
// and OpenRouter's top-level cost, rather than provider.Usage's flat shape.
type sseUsage struct {
	PromptTokens            int                  `json:"prompt_tokens"`
	CompletionTokens        int                  `json:"completion_tokens"`
	TotalTokens             int                  `json:"total_tokens"`
	Cost                    *float64             `json:"cost,omitempty"`
	PromptTokensDetails     *sseCachedDetails    `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *sseReasoningDetails `json:"completion_tokens_details,omitempty"`
}

type sseCachedDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type sseReasoningDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// usageWire converts the provider-facing usage into the OpenAI wire shape. A
// cost is only emitted when the usage says the provider reported it.
func usageWire(u *provider.Usage) *sseUsage {
	out := &sseUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.CachedTokens != 0 {
		out.PromptTokensDetails = &sseCachedDetails{CachedTokens: u.CachedTokens}
	}
	if u.ReasoningTokens != 0 {
		out.CompletionTokensDetails = &sseReasoningDetails{ReasoningTokens: u.ReasoningTokens}
	}
	if u.CostSource == "provider" {
		cost := u.CostUSD
		out.Cost = &cost
	}
	return out
}

type sseChoice struct {
	Index int      `json:"index"`
	Delta sseDelta `json:"delta"`
}

type sseDelta struct {
	Role      string        `json:"role,omitempty"`
	Content   string        `json:"content,omitempty"`
	Reasoning string        `json:"reasoning_content,omitempty"`
	ToolCalls []sseToolCall `json:"tool_calls,omitempty"`
}

type sseToolCall struct {
	Index    int         `json:"index"`
	ID       string      `json:"id,omitempty"`
	Type     string      `json:"type,omitempty"`
	Function sseFunction `json:"function"`
}

type sseFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// writeEvent writes one `data: {json}` SSE event and flushes it.
func writeEvent(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("testkit: marshal SSE chunk: %v", err))
	}
	io.WriteString(w, "data: "+string(data)+"\n\n")
	flush(w)
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
