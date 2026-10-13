// Package ssestream is the shared POST + Server-Sent Events transport for the
// OpenCode Go dialects. It owns the connect and idle timeouts, the line cap,
// and the mapping from HTTP status to provider.Error, so the Responses and
// Messages endpoints fail the same way as chat-completions.
package ssestream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AutumnsGrove/nova/provider"
)

// Defaults for the timeouts and the line cap. A zero in Config means "use the
// default", so a caller cannot accidentally ask for no timeout at all.
const (
	defaultConnectTimeout = 30 * time.Second
	defaultIdleTimeout    = 120 * time.Second
	defaultMaxLineBytes   = 8 << 20
	// maxErrorBody caps how much of a failed response body is read.
	maxErrorBody = 1 << 20
)

// Config configures a stream.
type Config struct {
	HTTPClient     *http.Client
	ConnectTimeout time.Duration
	IdleTimeout    time.Duration
	MaxLineBytes   int
}

// Event is one SSE event: the event name (empty for a plain data event) and
// the joined data payload without the "data:" prefix.
type Event struct {
	Name string
	Data []byte
}

// Stream is one open SSE response.
type Stream struct {
	body      io.ReadCloser
	reader    *bufio.Reader
	ctx       context.Context
	caller    context.Context
	cancel    context.CancelFunc
	max       int
	idleGap   time.Duration
	idleTimer *time.Timer
	idleFired *atomic.Bool
	sawEvent  bool
	done      bool
}

// lineTooLong is the error for a line past MaxLineBytes.
type lineTooLong struct{ limit int }

func (e *lineTooLong) Error() string {
	return fmt.Sprintf("ssestream: line exceeds the %d-byte limit", e.limit)
}

// errTruncatedLine marks a body that ended mid-line.
var errTruncatedLine = errors.New("ssestream: stream ended mid-line")

// Open posts body to url and returns an SSE stream. A non-2xx status is a
// *provider.Error built with provider.Classify. The caller must Close.
func Open(ctx context.Context, cfg Config, url string, headers http.Header, body []byte) (*Stream, error) {
	cfg = withDefaults(cfg)
	reqCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ssestream: build request: %w", err)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/event-stream")
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	var timedOut atomic.Bool
	timer := time.AfterFunc(cfg.ConnectTimeout, func() {
		timedOut.Store(true)
		cancel()
	})
	resp, err := client.Do(req)
	timer.Stop()
	if err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if timedOut.Load() {
			return nil, &provider.Error{Message: "connect timeout", Retryable: true, Err: err}
		}
		return nil, &provider.Error{Message: "request failed", Retryable: true, Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data := readCapped(resp.Body, maxErrorBody)
		resp.Body.Close()
		cancel()
		return nil, httpError(resp.StatusCode, resp.Header, data)
	}

	idleFired := &atomic.Bool{}
	s := &Stream{
		body:    resp.Body,
		reader:  bufio.NewReaderSize(resp.Body, cfg.MaxLineBytes+2),
		ctx:     reqCtx,
		caller:  ctx,
		cancel:  cancel,
		max:     cfg.MaxLineBytes,
		idleGap: cfg.IdleTimeout,
	}
	s.idleFired = idleFired
	s.idleTimer = time.AfterFunc(cfg.IdleTimeout, func() {
		idleFired.Store(true)
		cancel()
	})
	return s, nil
}

// Next returns the next event; io.EOF means the stream ended. Context
// cancellation returns ctx.Err() unwrapped; a connect or idle timeout returns
// a retryable *provider.Error.
func (s *Stream) Next() (Event, error) {
	if s.done {
		return Event{}, io.EOF
	}
	var name string
	var data []string
	for {
		line, err := s.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(data) > 0 || name != "" {
					return s.emit(name, data)
				}
				if s.sawEvent {
					s.done = true
					return Event{}, io.EOF
				}
				return Event{}, &provider.Error{Message: "empty stream", Retryable: true}
			}
			return Event{}, err
		}
		if line == "" {
			if len(data) == 0 && name == "" {
				continue
			}
			return s.emit(name, data)
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			if value == "[DONE]" {
				s.done = true
				return Event{}, io.EOF
			}
			data = append(data, value)
		case "id", "retry":
			// Accepted and ignored.
		}
	}
}

// emit finalizes one event and marks that the stream has produced output.
func (s *Stream) emit(name string, data []string) (Event, error) {
	s.sawEvent = true
	return Event{Name: name, Data: []byte(strings.Join(data, "\n"))}, nil
}

// readLine reads one line without its terminator.
func (s *Stream) readLine() (string, error) {
	frag, err := s.reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", &lineTooLong{limit: s.max}
	}
	if err != nil {
		if s.caller.Err() != nil {
			return "", s.caller.Err()
		}
		if s.idleFired.Load() {
			return "", &provider.Error{Message: "stream idle timeout", Retryable: true, Err: err}
		}
		if len(frag) == 0 {
			return "", io.EOF
		}
		return "", errTruncatedLine
	}
	s.idleTimer.Reset(s.idleTimeout())
	line := strings.TrimSuffix(string(frag), "\n")
	return strings.TrimSuffix(line, "\r"), nil
}

// idleTimeout returns the configured idle gap. The timer's reset needs it, so
// it is stored on the stream at Open time.
func (s *Stream) idleTimeout() time.Duration {
	if s.idleGap <= 0 {
		return defaultIdleTimeout
	}
	return s.idleGap
}

// Close releases the connection and stops the idle timer.
func (s *Stream) Close() error {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.cancel()
	return s.body.Close()
}

// withDefaults fills the zero config fields.
func withDefaults(cfg Config) Config {
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = defaultIdleTimeout
	}
	if cfg.MaxLineBytes == 0 {
		cfg.MaxLineBytes = defaultMaxLineBytes
	}
	return cfg
}

// readCapped reads at most max bytes, ignoring read errors.
func readCapped(r io.Reader, max int) []byte {
	data, _ := io.ReadAll(io.LimitReader(r, int64(max)))
	return data
}

// htmlTag strips markup from a non-JSON error body.
var htmlTag = regexp.MustCompile(`<[^>]*>`)

// httpError builds a provider.Error for a non-2xx response. The message comes
// from error.message in a JSON body, else the first 500 bytes of the body with
// HTML tags stripped.
func httpError(status int, header http.Header, body []byte) *provider.Error {
	retryable, retryAfter := provider.Classify(status, header.Get("Retry-After"))
	message := ""
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Message != "" {
		message = parsed.Error.Message
	} else {
		text := htmlTag.ReplaceAllString(string(body), "")
		text = strings.Join(strings.Fields(text), " ")
		if len(text) > 500 {
			text = text[:500]
		}
		message = text
	}
	return &provider.Error{Status: status, Message: message, Retryable: retryable, RetryAfter: retryAfter}
}
