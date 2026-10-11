// Package serve exposes a mode.Server over HTTP and server-sent events. It is a
// thin transport: commands in as JSON, events out as SSE, with a bearer token
// and DNS-rebinding defences in front because every request is untrusted.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/proto"
)

const (
	defaultListen    = "127.0.0.1:8080"
	defaultRingSize  = 1000
	defaultHeartbeat = 15 * time.Second
	defaultMaxBody   = 16 << 20
	defaultHeader    = 10 * time.Second
	idleTimeout      = 120 * time.Second
	writeTimeout     = 30 * time.Second
	shutdownGrace    = 5 * time.Second
	// NoTime is a sentinel meaning "no write deadline", used on the event
	// route where a stream legitimately stays open.
	NoTime time.Duration = 0
)

// Clock is the time seam for the heartbeat, so tests can drive it.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Options configures the HTTP transport.
type Options struct {
	Listen       string        // default "127.0.0.1:8080"
	Token        string        // bearer token; may come from NOVA_SERVE_TOKEN
	AllowedHosts []string      // extra Host header values accepted
	CORSOrigin   string        // a single exact origin, or ""
	RingSize     int           // events kept for resume; default 1000
	Heartbeat    time.Duration // default 15s
	MaxBody      int64         // default 16 MiB

	// Clock and headerTimeout are test seams. Production callers leave them
	// zero and get the real clock and the documented 10s header timeout.
	Clock         Clock
	headerTimeout time.Duration
}

// normalize fills defaults and validates the configuration.
func (o Options) normalize() (Options, error) {
	if o.Listen == "" {
		o.Listen = defaultListen
	}
	if o.Token == "" {
		o.Token = os.Getenv("NOVA_SERVE_TOKEN")
	}
	if o.RingSize <= 0 {
		o.RingSize = defaultRingSize
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = defaultHeartbeat
	}
	if o.MaxBody <= 0 {
		o.MaxBody = defaultMaxBody
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.headerTimeout <= 0 {
		o.headerTimeout = defaultHeader
	}
	if err := validateCORS(o.CORSOrigin); err != nil {
		return Options{}, err
	}
	return o, nil
}

// server is the transport. It owns the event ring and the UI forwarding, not
// the session: the mode.Server does.
type server struct {
	mode *mode.Server
	opt  Options
	mux  *http.ServeMux

	mu      sync.Mutex
	ring    []streamItem
	seq     int64
	clients map[chan streamItem]struct{}
	done    chan struct{}
	closed  sync.Once

	pendingMu sync.Mutex
	pending   map[string]struct{}
}

// Handler builds the HTTP handler over s. It starts the event pump, which a
// caller that also binds should stop by cancelling the context passed to
// ListenAndServe; a bare handler keeps it for the process lifetime.
func Handler(s *mode.Server, opt Options) (http.Handler, error) {
	srv, err := newServer(s, opt)
	if err != nil {
		return nil, err
	}
	srv.start(context.Background())
	return srv, nil
}

// newServer validates options and wires the routes without starting the pump.
func newServer(s *mode.Server, opt Options) (*server, error) {
	if s == nil {
		return nil, fmt.Errorf("serve: mode.Server is required")
	}
	norm, err := opt.normalize()
	if err != nil {
		return nil, err
	}
	srv := &server{
		mode:    s,
		opt:     norm,
		clients: map[chan streamItem]struct{}{},
		done:    make(chan struct{}),
		pending: map[string]struct{}{},
	}
	srv.mux = http.NewServeMux()
	srv.mux.HandleFunc("POST /v1/commands", srv.handleCommands)
	srv.mux.HandleFunc("GET /v1/events", srv.handleEvents)
	srv.mux.HandleFunc("POST /v1/ui_response", srv.handleUIResponse)
	srv.mux.HandleFunc("GET /v1/state", srv.handleState)
	srv.mux.HandleFunc("GET /healthz", srv.handleHealthz)
	srv.mode.SetUIHandler(srv.forwardUI)
	return srv, nil
}

// start pumps server events into the ring until done or ctx.
func (s *server) start(ctx context.Context) {
	events, cancel := s.mode.Subscribe(4096)
	go func() {
		defer cancel()
		for {
			select {
			case <-s.done:
				return
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				s.publish(ev)
			}
		}
	}()
}

// close stops the pump and releases every event stream. It is idempotent.
func (s *server) close() {
	s.closed.Do(func() { close(s.done) })
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		close(ch)
		delete(s.clients, ch)
	}
}

// ServeHTTP applies the transport defences, then routes.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer s.recover500(w)
	if s.opt.CORSOrigin != "" {
		s.corsHeaders(w, r)
		if r.Method == http.MethodOptions && isRoute(r.URL.Path) {
			s.handlePreflight(w)
			return
		}
	}
	if r.URL.Path != "/healthz" && !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	if (s.opt.Token == "" || len(s.opt.AllowedHosts) > 0) && r.Method != http.MethodOptions &&
		!hostAllowed(r.Host, s.opt.AllowedHosts) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "")
		return
	}
	s.mux.ServeHTTP(w, r)
}

// authorized reports whether the request may proceed. An empty token disables
// the check; the token is never read from the query string.
func (s *server) authorized(r *http.Request) bool {
	if s.opt.Token == "" {
		return true
	}
	return constantTimeEqual(bearer(r.Header.Get("Authorization")), s.opt.Token)
}

// recover500 turns a handler panic into a bare 500 with no stack text.
func (s *server) recover500(w http.ResponseWriter) {
	if recover() != nil {
		writeJSONError(w, http.StatusInternalServerError, proto.CodeInternal, "internal error")
	}
}

// isRoute reports whether path is one of the four protocol routes.
func isRoute(path string) bool {
	switch path {
	case "/v1/commands", "/v1/events", "/v1/ui_response", "/v1/state":
		return true
	}
	return false
}

// handlePreflight answers an OPTIONS preflight for one exact origin.
func (s *server) handlePreflight(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// handleCommands takes one command frame and returns its response frame.
func (s *server) handleCommands(w http.ResponseWriter, r *http.Request) {
	setDeadline(w, writeTimeout)
	if !isJSON(r.Header.Get("Content-Type")) {
		writeJSONError(w, http.StatusUnsupportedMediaType, proto.CodeUnsupported, "Content-Type must be application/json")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.opt.MaxBody))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, proto.CodeBadRequest, "request body too large")
			return
		}
		writeJSONError(w, http.StatusBadRequest, proto.CodeBadRequest, "invalid request body")
		return
	}
	var cmd proto.Command
	if err := json.Unmarshal(data, &cmd); err != nil {
		writeJSONError(w, http.StatusBadRequest, proto.CodeBadRequest, "invalid JSON")
		return
	}
	writeJSON(w, http.StatusOK, s.mode.Handle(r.Context(), cmd))
}

// handleState is get_state, the same command the protocol defines.
func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	setDeadline(w, writeTimeout)
	writeJSON(w, http.StatusOK, s.mode.Handle(r.Context(), proto.Command{Type: proto.CmdGetState}))
}

// handleHealthz is the one route that needs no token and has no detail.
func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	setDeadline(w, writeTimeout)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

// handleUIResponse routes an answer to the request that is waiting for it.
func (s *server) handleUIResponse(w http.ResponseWriter, r *http.Request) {
	setDeadline(w, writeTimeout)
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, proto.CodeBadRequest, "invalid request body")
		return
	}
	var resp proto.UIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		writeJSONError(w, http.StatusBadRequest, proto.CodeBadRequest, "invalid JSON")
		return
	}
	if !s.takePending(resp.ID) {
		writeJSONError(w, http.StatusNotFound, proto.CodeNotFound, "unknown ui_request id")
		return
	}
	s.mode.AnswerUI(resp)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// forwardUI streams a UI request to the event clients and marks its id live.
// The answer arrives later as a separate POST /v1/ui_response.
func (s *server) forwardUI(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	s.pendingMu.Lock()
	s.pending[req.ID] = struct{}{}
	s.pendingMu.Unlock()
	s.publish(req)
	return proto.UIResponse{}, nil
}

// takePending removes id and reports whether it was live.
func (s *server) takePending(id string) bool {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if _, ok := s.pending[id]; !ok {
		return false
	}
	delete(s.pending, id)
	return true
}

// ListenAndServe binds, serves, and shuts down with ctx. It refuses to bind a
// non-loopback address without a token, before it opens the socket.
func ListenAndServe(ctx context.Context, s *mode.Server, opt Options) error {
	srv, err := newServer(s, opt)
	if err != nil {
		return err
	}
	if !loopbackListen(srv.opt.Listen) && srv.opt.Token == "" {
		return fmt.Errorf("serve: refusing to listen on %s without a token", srv.opt.Listen)
	}
	ln, err := net.Listen("tcp", srv.opt.Listen)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: srv.opt.headerTimeout,
		IdleTimeout:       idleTimeout,
	}
	srv.start(ctx)
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()
	select {
	case <-ctx.Done():
		srv.close()
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		srv.close()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// isJSON reports whether a Content-Type header is application/json.
func isJSON(ct string) bool {
	media, _, err := mime.ParseMediaType(ct)
	return err == nil && media == "application/json"
}

// setDeadline bounds a non-streaming response. A zero duration clears it.
func setDeadline(w http.ResponseWriter, d time.Duration) {
	if d == NoTime {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}

// writeJSON writes v as a JSON response with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError writes a response-shaped error with an HTTP status.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, proto.Response{
		Type:  proto.FrameResponse,
		OK:    false,
		Error: &proto.Error{Code: code, Message: message},
	})
}
