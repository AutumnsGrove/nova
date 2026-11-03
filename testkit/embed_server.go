package testkit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// EmbeddingServerTurn scripts one HTTP answer from an embeddings endpoint.
type EmbeddingServerTurn struct {
	// Status is the HTTP status. Zero means 200.
	Status int
	// Body is the raw response body. Empty means "{}".
	Body string
	// Headers are added to the response, for a Retry-After or a content type.
	Headers map[string]string
	// Hang blocks until the request is cancelled, for a cancellation test.
	Hang bool
}

// EmbeddingServer is a local HTTP server that answers the embeddings endpoints:
// POST {base}/embeddings and POST /api/embed. It records each request and plays
// the scripted turns in order.
type EmbeddingServer struct {
	URL string

	ts *httptest.Server

	mu    sync.Mutex
	turns []EmbeddingServerTurn
	next  int
	reqs  []RecordedRequest
}

// NewEmbeddingServer starts a server that plays the turns in order. It is
// closed when the test's cleanup runs.
func NewEmbeddingServer(t testing.TB, turns ...EmbeddingServerTurn) *EmbeddingServer {
	t.Helper()
	s := &EmbeddingServer{turns: turns}
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.ts.URL
	t.Cleanup(s.ts.Close)
	return s
}

// Requests returns the recorded requests, copied so callers cannot mutate them.
func (s *EmbeddingServer) Requests() []RecordedRequest {
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

func (s *EmbeddingServer) serve(w http.ResponseWriter, r *http.Request) {
	if !knownEmbeddingRequest(r.Method, r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, RecordedRequest{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	have := s.next < len(s.turns)
	var turn EmbeddingServerTurn
	if have {
		turn = s.turns[s.next]
		s.next++
	}
	s.mu.Unlock()

	if !have {
		http.Error(w, `{"error":{"message":"testkit: embedding script exhausted"}}`, http.StatusInternalServerError)
		return
	}
	if turn.Hang {
		<-r.Context().Done()
		return
	}
	status := turn.Status
	if status == 0 {
		status = http.StatusOK
	}
	text := turn.Body
	if text == "" {
		text = "{}"
	}
	for name, value := range turn.Headers {
		w.Header().Set(name, value)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, text)
}

// knownEmbeddingRequest reports whether a method and path is one of the
// embeddings surfaces: an OpenAI-compatible POST /embeddings, Ollama's POST
// /api/embed, or the GET model roster.
func knownEmbeddingRequest(method, path string) bool {
	if method == http.MethodGet {
		return strings.HasSuffix(path, "/models")
	}
	return method == http.MethodPost && (strings.HasSuffix(path, "/embeddings") || strings.HasSuffix(path, "/api/embed"))
}
