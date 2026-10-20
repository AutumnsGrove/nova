// Package ssestream is the shared POST + Server-Sent Events transport for every
// streaming dialect: chat-completions (openaicompat), Responses, and Messages.
// It owns the request plumbing (headers, credentials), the connect and idle
// timeouts, the line cap, and the mapping from HTTP status and transport
// failure to provider.Error, so all of them fail the same way.
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
	// readBufferBytes is the reader's buffer. A line longer than it is
	// accumulated fragment by fragment up to the line cap, so a stream pays for
	// a long line only when it sends one.
	readBufferBytes = 64 << 10
	// maxErrorBody caps how much of a failed response body is read.
	maxErrorBody = 1 << 20
	// maxErrorMessage caps the prose kept from an error body.
	maxErrorMessage = 500
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
	// Line is the 1-based number of the last data line in the event, so a
	// caller can name the line a bad payload came from.
	Line int
}

// Decode unmarshals the payload into v. A payload that is not valid JSON is a
// non-retryable provider error naming the line: the server is speaking
// something else, and skipping the event would silently drop part of the reply.
func (e Event) Decode(v any) error {
	if err := json.Unmarshal(e.Data, v); err != nil {
		return &provider.Error{Message: fmt.Sprintf("invalid JSON on line %d: %v", e.Line, err)}
	}
	return nil
}

// Stream is one open SSE response.
type Stream struct {
	body      io.ReadCloser
	reader    *bufio.Reader
	caller    context.Context
	cancel    context.CancelFunc
	max       int
	idleGap   time.Duration
	idleTimer *time.Timer
	idleFired atomic.Bool
	sawEvent  bool
	sawDone   bool
	lines     int
	dataLine  int // line number of the latest data line
	done      bool
}

// LineTooLongError is the error for a line past MaxLineBytes. It is a fault of
// the server or the configuration, never worth retrying.
type LineTooLongError struct{ Limit int }

func (e *LineTooLongError) Error() string {
	return fmt.Sprintf("ssestream: line exceeds the %d-byte limit", e.Limit)
}

// ErrTruncatedLine marks a body that ended mid-line: the connection was cut
// inside an event, not closed cleanly after one.
var ErrTruncatedLine = errors.New("ssestream: stream ended mid-line")

// Target is where and how one dialect posts its requests. Every dialect builds
// the same request, differing only in the endpoint and how the key is sent.
type Target struct {
	Config
	// Dialect names the provider package in error messages.
	Dialect string
	// URL is the full endpoint.
	URL string
	// Headers are sent on every request.
	Headers map[string]string
	// Fixed headers are protocol constants that a per-request header cannot
	// override, such as a required API version.
	Fixed map[string]string
	// APIKey is the credential. APIKeyFunc, when set, supplies it per request
	// and wins, so a key can rotate without rebuilding the provider.
	APIKey     string
	APIKeyFunc func(ctx context.Context) (string, error)
	// KeyHeader and KeyPrefix say how the key is sent: "Authorization" with
	// "Bearer " when KeyHeader is empty.
	KeyHeader string
	KeyPrefix string
}

// Check rejects a request that cannot be sent: a cancelled context or a missing
// model. It runs before anything is built so the failure costs nothing.
func (t Target) Check(ctx context.Context, model string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if model == "" {
		return fmt.Errorf("%s: model is required", t.Dialect)
	}
	if t.URL == "" {
		return fmt.Errorf("%s: base URL is required", t.Dialect)
	}
	return nil
}

// Open posts body and returns the SSE stream. Headers apply in this order, a
// later one replacing an earlier: the target's, the request's extra headers,
// the fixed ones, and the credential last so a stray header cannot shadow it.
func (t Target) Open(ctx context.Context, extra map[string]string, body []byte) (*Stream, error) {
	key := t.APIKey
	if t.APIKeyFunc != nil {
		var err error
		if key, err = t.APIKeyFunc(ctx); err != nil {
			return nil, &provider.Error{Message: "resolving API key", Err: err}
		}
	}
	headers := http.Header{}
	for _, set := range []map[string]string{t.Headers, extra, t.Fixed} {
		for k, v := range set {
			headers.Set(k, v)
		}
	}
	if key != "" {
		name, prefix := t.KeyHeader, t.KeyPrefix
		if name == "" {
			name, prefix = "Authorization", "Bearer "
		}
		headers.Set(name, prefix+key)
	}
	return Open(ctx, t.Config, t.URL, headers, body)
}

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
	timer := time.AfterFunc(cfg.ConnectTimeout, cancel)
	resp, err := client.Do(req)
	// A timer that has fired cancelled the request, even if the response headers
	// arrived in the same instant, so what Stop reports decides the cause.
	timedOut := !timer.Stop()
	if err != nil || timedOut {
		if resp != nil {
			_ = resp.Body.Close()
		}
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if timedOut {
			return nil, &provider.Error{Message: "connect timeout", Retryable: true, Err: err}
		}
		return nil, &provider.Error{Message: "request failed", Retryable: true, Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// A read error leaves a short body, and a short body still gives the
		// status-based error its best message.
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		cancel()
		return nil, httpError(resp.StatusCode, resp.Header, data, credentials(headers))
	}
	return newStream(resp.Body, ctx, cancel, cfg), nil
}

// newStream wraps an open response body.
func newStream(body io.ReadCloser, caller context.Context, cancel context.CancelFunc, cfg Config) *Stream {
	s := &Stream{
		body:    body,
		reader:  bufio.NewReaderSize(body, readBufferBytes),
		caller:  caller,
		cancel:  cancel,
		max:     cfg.MaxLineBytes,
		idleGap: cfg.IdleTimeout,
	}
	s.idleTimer = time.AfterFunc(cfg.IdleTimeout, func() {
		s.idleFired.Store(true)
		cancel()
	})
	return s
}

// Each calls fn for every event that carries data until fn reports stop, the
// stream ends cleanly (nil), or it fails. An event with no data, such as a bare
// event name, has nothing to decode and is skipped.
func (s *Stream) Each(fn func(Event) (stop bool, err error)) error {
	for {
		ev, err := s.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(ev.Data) == 0 {
			continue
		}
		if stop, err := fn(ev); err != nil || stop {
			return err
		}
	}
}

// Next returns the next event; io.EOF means the stream ended. Context
// cancellation returns ctx.Err() unwrapped. Every other failure is a
// *provider.Error: a connect or idle timeout or a line cut mid-event is
// retryable, a line over the cap is not.
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
					return s.emit(name, data), nil
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
			return s.emit(name, data), nil
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
				s.done, s.sawDone = true, true
				if len(data) > 0 {
					return s.emit(name, data), nil // data pending in this event is delivered first
				}
				return Event{}, io.EOF
			}
			data = append(data, value)
			s.dataLine = s.lines
		}
		// Every other field, including id and retry, is accepted and ignored.
	}
}

// emit finalizes one event and marks that the stream has produced output.
func (s *Stream) emit(name string, data []string) Event {
	s.sawEvent = true
	return Event{Name: name, Data: []byte(strings.Join(data, "\n")), Line: s.dataLine}
}

// readLine reads one line without its terminator. A line longer than the
// reader's buffer is gathered from fragments, but never past the cap.
func (s *Stream) readLine() (string, error) {
	s.lines++
	var long []byte
	for {
		frag, err := s.reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// +2 leaves room for the "\r\n" that follows a line of exactly max bytes.
			if long = append(long, frag...); len(long) > s.max+2 {
				return "", s.tooLong()
			}
			continue
		}
		if err != nil {
			return "", s.readFailure(err, len(long)+len(frag))
		}
		line := frag
		if long != nil {
			line = append(long, frag...)
		}
		s.idleTimer.Reset(s.idleGap)
		line = bytes.TrimSuffix(line, []byte("\n"))
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) > s.max {
			return "", s.tooLong()
		}
		return string(line), nil
	}
}

func (s *Stream) tooLong() error {
	limit := &LineTooLongError{Limit: s.max}
	return &provider.Error{Message: limit.Error(), Err: limit}
}

// readFailure classifies a failed read. buffered is how many bytes of the
// current line were read before it failed.
func (s *Stream) readFailure(err error, buffered int) error {
	if s.caller.Err() != nil {
		return s.caller.Err()
	}
	if s.idleFired.Load() {
		return &provider.Error{Message: "stream idle timeout", Retryable: true, Err: err}
	}
	if buffered == 0 {
		return io.EOF
	}
	return &provider.Error{Message: "stream ended mid-event", Retryable: true, Err: ErrTruncatedLine}
}

// SawDone reports whether the server sent the [DONE] sentinel, which tells a
// clean finish from a connection that merely closed.
func (s *Stream) SawDone() bool { return s.sawDone }

// Close releases the connection and stops the idle timer. The error from
// closing a response body that is being abandoned is never actionable, so
// there is none to check.
func (s *Stream) Close() {
	s.idleTimer.Stop()
	s.cancel()
	_ = s.body.Close()
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
