package ssestream

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// sseServer serves a fixed SSE body, flushing after each write.
func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, chunk := range strings.SplitAfter(body, "\x00") {
			_, _ = io.WriteString(w, strings.ReplaceAll(chunk, "\x00", ""))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// open posts an empty body to url with cfg.
func open(ctx context.Context, t *testing.T, cfg Config, url string) (*Stream, error) {
	t.Helper()
	return Open(ctx, cfg, url, http.Header{}, []byte(`{}`))
}

// collect drains a stream into events.
func collect(t *testing.T, s *Stream) []Event {
	t.Helper()
	var out []Event
	for {
		ev, err := s.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, ev)
	}
}

func TestSSEStream_TMO08_LineForms(t *testing.T) {
	body := ":comment\n\n" +
		"event: ping\ndata: one\n\n" +
		"data:two\r\n\r\n" +
		"data: a\ndata: b\n\n" +
		"data: [DONE]\n\n"
	srv := sseServer(t, body)
	s, err := open(context.Background(), t, Config{}, srv.URL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	events := collect(t, s)
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3: %+v", len(events), events)
	}
	if events[0].Name != "ping" || string(events[0].Data) != "one" {
		t.Fatalf("event 0 = %+v", events[0])
	}
	if string(events[1].Data) != "two" {
		t.Fatalf("event 1 = %+v", events[1])
	}
	if string(events[2].Data) != "a\nb" {
		t.Fatalf("event 2 = %+v", events[2])
	}
}

func TestSSEStream_TMO09_DoneAndClose(t *testing.T) {
	t.Run("done", func(t *testing.T) {
		srv := sseServer(t, "data: one\n\ndata: [DONE]\n\n")
		s, err := open(context.Background(), t, Config{}, srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := s.Next(); err != nil {
			t.Fatalf("first event: %v", err)
		}
		if _, err := s.Next(); !errors.Is(err, io.EOF) {
			t.Fatalf("second Next = %v, want io.EOF", err)
		}
	})
	t.Run("clean close", func(t *testing.T) {
		srv := sseServer(t, "data: one\n\n")
		s, err := open(context.Background(), t, Config{}, srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := s.Next(); err != nil {
			t.Fatalf("first event: %v", err)
		}
		if _, err := s.Next(); !errors.Is(err, io.EOF) {
			t.Fatalf("second Next = %v, want io.EOF", err)
		}
	})
}

func TestSSEStream_TMO01_ConnectTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	_, err := open(context.Background(), t, Config{ConnectTimeout: 50 * time.Millisecond}, srv.URL)
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable || !strings.Contains(perr.Message, "connect timeout") {
		t.Fatalf("err = %v, want a retryable connect timeout", err)
	}
}

func TestSSEStream_TMO02_IdleTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = io.WriteString(w, "data: one\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	s, err := open(context.Background(), t, Config{IdleTimeout: 50 * time.Millisecond}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Next(); err != nil {
		t.Fatalf("first event: %v", err)
	}
	_, err = s.Next()
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable {
		t.Fatalf("second Next = %v, want a retryable idle timeout", err)
	}
}

func TestSSEStream_TMO03_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	_, err := open(context.Background(), t, Config{ConnectTimeout: time.Second}, url)
	var perr *provider.Error
	if !errors.As(err, &perr) || !perr.Retryable || perr.Err == nil {
		t.Fatalf("err = %v, want a retryable error with Err set", err)
	}
}

func TestSSEStream_TMO04_CancelDuringConnect(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	began := time.Now()
	_, err := open(ctx, t, Config{ConnectTimeout: 5 * time.Second}, srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(began) > time.Second {
		t.Fatal("cancel did not return promptly")
	}
}

func TestSSEStream_TMO05_CancelMidStream(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = io.WriteString(w, "data: one\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	s, err := open(ctx, t, Config{IdleTimeout: 5 * time.Second}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Next(); err != nil {
		t.Fatalf("first event: %v", err)
	}
	cancel()
	if _, err := s.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("second Next = %v, want context.Canceled", err)
	}
}

func TestSSEStream_TMO06_Statuses(t *testing.T) {
	t.Run("retryable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"slow down"}}`)
		}))
		t.Cleanup(srv.Close)
		_, err := open(context.Background(), t, Config{}, srv.URL)
		var perr *provider.Error
		if !errors.As(err, &perr) || !perr.Retryable || perr.RetryAfter != 2*time.Second {
			t.Fatalf("err = %v, want retryable with RetryAfter 2s", err)
		}
		if perr.Message != "slow down" {
			t.Fatalf("message = %q", perr.Message)
		}
	})
	t.Run("non-retryable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, "<html><body>nope</body></html>")
		}))
		t.Cleanup(srv.Close)
		_, err := open(context.Background(), t, Config{}, srv.URL)
		var perr *provider.Error
		if !errors.As(err, &perr) || perr.Retryable {
			t.Fatalf("err = %v, want non-retryable", err)
		}
		if strings.Contains(perr.Message, "<") {
			t.Fatalf("message = %q, want HTML stripped", perr.Message)
		}
	})
}

func TestSSEStream_TMO07_LineTooLong(t *testing.T) {
	big := strings.Repeat("x", 2048)
	srv := sseServer(t, "data: "+big+"\n\n")
	s, err := open(context.Background(), t, Config{MaxLineBytes: 1024}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Next()
	var tooLong *LineTooLongError
	if !errors.As(err, &tooLong) {
		t.Fatalf("err = %v, want a line-too-long error", err)
	}
	if !strings.Contains(err.Error(), "1024") {
		t.Fatalf("error should name the limit: %v", err)
	}
}

func TestSSEStream_TMO10_NoLeaks(t *testing.T) {
	testkit.NoLeaks(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	s, err := open(context.Background(), t, Config{IdleTimeout: 30 * time.Millisecond}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Next(); err == nil {
		t.Fatal("expected an idle timeout")
	}
	_ = s.Close()
}

// newTestStream builds a stream over arbitrary bytes for the fuzz target.
func newTestStream(data []byte) *Stream {
	idleFired := &atomic.Bool{}
	return &Stream{
		body:      io.NopCloser(bytes.NewReader(data)),
		reader:    bufio.NewReaderSize(bytes.NewReader(data), defaultMaxLineBytes+2),
		ctx:       context.Background(),
		caller:    context.Background(),
		cancel:    func() {},
		max:       defaultMaxLineBytes,
		idleGap:   time.Hour,
		idleFired: idleFired,
		idleTimer: time.AfterFunc(time.Hour, func() {}),
	}
}

func FuzzSSEStream(f *testing.F) {
	f.Add([]byte("data: hello\n\n"))
	f.Add([]byte("event: x\ndata: a\ndata: b\n\n"))
	f.Add([]byte(": comment\r\ndata:two\r\n\r\n"))
	f.Add([]byte("data: [DONE]\n\n"))
	f.Add([]byte("\x00\xff"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s := newTestStream(data)
		defer s.Close()
		for i := 0; i < 1000; i++ {
			if _, err := s.Next(); err != nil {
				return
			}
		}
	})
}

func TestSSEStream_TMO11_ErrorBodyShapes(t *testing.T) {
	cases := []struct {
		name, body, wantCode, wantMessage string
	}{
		{"nested code and message", `{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`, "rate_limit_exceeded", "slow down"},
		{"numeric nested code", `{"error":{"code":429,"message":"slow down"}}`, "429", "slow down"},
		{"top-level message and code", `{"message":"top level","code":"E1"}`, "E1", "top level"},
		{"prose", "<p>gateway  down</p>", "", "gateway down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)
			_, err := open(context.Background(), t, Config{}, srv.URL)
			var perr *provider.Error
			if !errors.As(err, &perr) {
				t.Fatalf("err = %v, want *provider.Error", err)
			}
			if perr.Code != tc.wantCode || perr.Message != tc.wantMessage {
				t.Fatalf("code = %q message = %q, want %q and %q", perr.Code, perr.Message, tc.wantCode, tc.wantMessage)
			}
		})
	}
}

func TestSSEStream_TMO12_TruncatedLineIsExported(t *testing.T) {
	srv := sseServer(t, "data: one\n\ndata: cut")
	s, err := open(context.Background(), t, Config{}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Next(); err != nil {
		t.Fatalf("first event: %v", err)
	}
	if _, err := s.Next(); !errors.Is(err, ErrTruncatedLine) {
		t.Fatalf("err = %v, want ErrTruncatedLine", err)
	}
}

func TestSSEStream_TMO13_LineTooLongIsExported(t *testing.T) {
	srv := sseServer(t, "data: "+strings.Repeat("x", 2048)+"\n\n")
	s, err := open(context.Background(), t, Config{MaxLineBytes: 1024}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Next()
	var tooLong *LineTooLongError
	if !errors.As(err, &tooLong) || tooLong.Limit != 1024 {
		t.Fatalf("err = %v, want *LineTooLongError with Limit 1024", err)
	}
}

func TestSSEStream_TMO14_LineCounter(t *testing.T) {
	srv := sseServer(t, ":c\n\ndata: a\n\ndata: b\ndata: c\n\n")
	s, err := open(context.Background(), t, Config{}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Line() != 0 {
		t.Fatalf("Line before reading = %d, want 0", s.Line())
	}
	if _, err := s.Next(); err != nil { // reads ":c", "", "data: a", ""
		t.Fatal(err)
	}
	if s.Line() != 4 {
		t.Fatalf("Line after first event = %d, want 4", s.Line())
	}
	if _, err := s.Next(); err != nil { // reads "data: b", "data: c", ""
		t.Fatal(err)
	}
	if s.Line() != 7 {
		t.Fatalf("Line after second event = %d, want 7", s.Line())
	}
}
