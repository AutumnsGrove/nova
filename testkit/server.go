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
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
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

	switch turn.Fault {
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
	if turn.Usage != nil {
		writeEvent(w, sseChunk{ID: "chatcmpl-test", Object: "chat.completion.chunk", Model: "testkit", Choices: []sseChoice{}, Usage: turn.Usage})
	}
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
	ID      string          `json:"id,omitempty"`
	Object  string          `json:"object,omitempty"`
	Model   string          `json:"model,omitempty"`
	Choices []sseChoice     `json:"choices"`
	Usage   *provider.Usage `json:"usage,omitempty"`
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
