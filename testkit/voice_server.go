package testkit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// VoiceServerTurn scripts one HTTP answer from a voice endpoint.
type VoiceServerTurn struct {
	// Status is the HTTP status. Zero means 200.
	Status int
	// Body is a JSON response body. Empty means "{}".
	Body string
	// Audio, when non-nil, is written as the raw response body, for a speech
	// endpoint that answers with bytes rather than JSON.
	Audio []byte
	// ContentType is the response Content-Type. It defaults to audio/mpeg for
	// an Audio turn and application/json otherwise.
	ContentType string
	// Headers are added to the response, for a generation id or a Retry-After.
	Headers map[string]string
	// Hang blocks until the request is cancelled, for a cancellation test.
	Hang bool
}

// VoiceServer is a local HTTP server that answers the voice surfaces: POST
// /audio/speech, POST /audio/transcriptions, GET /models and GET /generation.
// It records each request and plays the scripted turns in order across all of
// them, so a speak with a cost lookup scripts two turns.
type VoiceServer struct {
	URL string

	ts *httptest.Server

	mu    sync.Mutex
	turns []VoiceServerTurn
	next  int
	reqs  []RecordedRequest
}

// NewVoiceServer starts a server that plays the turns in order. It is closed
// when the test's cleanup runs.
func NewVoiceServer(t testing.TB, turns ...VoiceServerTurn) *VoiceServer {
	t.Helper()
	s := &VoiceServer{turns: turns}
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.ts.URL
	t.Cleanup(s.ts.Close)
	return s
}

// Requests returns the recorded requests, copied so callers cannot mutate them.
func (s *VoiceServer) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RecordedRequest, len(s.reqs))
	for i, r := range s.reqs {
		out[i] = RecordedRequest{
			Method: r.Method,
			Path:   r.Path,
			Query:  r.Query,
			Header: r.Header.Clone(),
			Body:   append([]byte(nil), r.Body...),
		}
	}
	return out
}

func (s *VoiceServer) serve(w http.ResponseWriter, r *http.Request) {
	if !knownVoiceRequest(r.Method, r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, RecordedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body})
	have := s.next < len(s.turns)
	var turn VoiceServerTurn
	if have {
		turn = s.turns[s.next]
		s.next++
	}
	s.mu.Unlock()

	if !have {
		http.Error(w, `{"error":{"message":"testkit: voice script exhausted"}}`, http.StatusInternalServerError)
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
	for name, value := range turn.Headers {
		w.Header().Set(name, value)
	}
	contentType := turn.ContentType
	if contentType == "" {
		if turn.Audio != nil {
			contentType = "audio/mpeg"
		} else {
			contentType = "application/json"
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if turn.Audio != nil {
		_, _ = w.Write(turn.Audio)
		return
	}
	text := turn.Body
	if text == "" {
		text = "{}"
	}
	_, _ = io.WriteString(w, text)
}

// knownVoiceRequest reports whether a method and path is one of the voice
// surfaces: the two audio endpoints, the model roster, one model's endpoint
// list, or the generation lookup.
func knownVoiceRequest(method, path string) bool {
	switch method {
	case http.MethodPost:
		return strings.HasSuffix(path, "/audio/speech") || strings.HasSuffix(path, "/audio/transcriptions")
	case http.MethodGet:
		return strings.HasSuffix(path, "/models") || strings.HasSuffix(path, "/generation") ||
			strings.HasSuffix(path, "/endpoints")
	}
	return false
}
