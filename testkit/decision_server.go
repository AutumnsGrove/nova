package testkit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// DecisionServerTurn scripts one HTTP answer from the Decisions API or the
// System One surface.
type DecisionServerTurn struct {
	// Status is the HTTP status. Zero means 200.
	Status int
	// Body is the raw response body. Empty means "{}".
	Body string
	// Headers are added to the response, for a Retry-After or a content type.
	Headers map[string]string
	// Hang blocks until the request is cancelled, for a timeout test.
	Hang bool
}

// DecisionServer is a local HTTP server that answers the decision-model
// endpoints: POST /api/alpha/decisions and POST {base}/systemone. It records
// each request and plays the scripted turns in order.
type DecisionServer struct {
	URL string

	ts *httptest.Server

	mu    sync.Mutex
	turns []DecisionServerTurn
	next  int
	reqs  []RecordedRequest
}

// NewDecisionServer starts a server that plays the turns in order. It is closed
// when the test's cleanup runs.
func NewDecisionServer(t testing.TB, turns ...DecisionServerTurn) *DecisionServer {
	t.Helper()
	s := &DecisionServer{turns: turns}
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.ts.URL
	t.Cleanup(s.ts.Close)
	return s
}

// Requests returns the recorded requests, copied so callers cannot mutate them.
func (s *DecisionServer) Requests() []RecordedRequest {
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

func (s *DecisionServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !knownDecisionPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, RecordedRequest{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	have := s.next < len(s.turns)
	var turn DecisionServerTurn
	if have {
		turn = s.turns[s.next]
		s.next++
	}
	s.mu.Unlock()

	if !have {
		http.Error(w, `{"error":{"message":"testkit: decision script exhausted"}}`, http.StatusInternalServerError)
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

// knownDecisionPath reports whether a POST path is one of the decision-model
// surfaces: OpenRouter's Decisions API, or the Jev-only System One endpoint
// under any API base.
func knownDecisionPath(path string) bool {
	return strings.HasSuffix(path, "/api/alpha/decisions") || strings.HasSuffix(path, "/systemone")
}
