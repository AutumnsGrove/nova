// Package serve exposes a mode.Server over HTTP and server-sent events. It is a
// thin transport: commands in as JSON, events out as SSE, with a bearer token
// and DNS-rebinding defences in front because every request is untrusted.
package serve

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/mode"
	"github.com/AutumnsGrove/nova/proto"
)

const (
	defaultListen    = "127.0.0.1:8080"
	defaultRingSize  = 1000
	defaultHeartbeat = 15 * time.Second
	defaultMaxBody   = 32 << 20
	defaultHeader    = 10 * time.Second
	idleTimeout      = 120 * time.Second
	writeTimeout     = 30 * time.Second
	shutdownGrace    = 5 * time.Second
)

// Options configures the HTTP transport.
type Options struct {
	Listen       string        // default "127.0.0.1:8080"
	Token        string        // bearer token; may come from NOVA_SERVE_TOKEN
	AllowedHosts []string      // extra Host header values accepted
	CORSOrigin   string        // a single exact origin, or ""
	RingSize     int           // events kept for resume; default 1000
	Heartbeat    time.Duration // default 15s
	MaxBody      int64         // default 16 MiB

	headerTimeout time.Duration // test seam; zero means the documented 10s
}

// apply fills defaults and validates the configuration. The heartbeat uses the
// real clock; a test that wants it sooner sets a small Heartbeat.
func (o *Options) apply() error {
	o.Listen = def(o.Listen, defaultListen)
	o.Token = def(o.Token, os.Getenv(auth.EnvServeToken))
	o.RingSize = def(o.RingSize, defaultRingSize)
	o.Heartbeat = def(o.Heartbeat, defaultHeartbeat)
	o.MaxBody = def(o.MaxBody, defaultMaxBody)
	o.headerTimeout = def(o.headerTimeout, defaultHeader)
	if o.CORSOrigin != "" {
		u, err := url.Parse(o.CORSOrigin)
		if o.CORSOrigin == "*" || err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("serve: CORSOrigin %q must be one exact origin", o.CORSOrigin)
		}
	}
	return nil
}

// def returns v, or d when v is the zero value.
func def[T comparable](v, d T) T {
	var zero T
	if v == zero {
		return d
	}
	return v
}

// server is the transport. It owns the event ring and UI forwarding, not the
// session: the mode.Server does.
type server struct {
	mode    *mode.Server
	opt     Options
	mux     *http.ServeMux
	mu      sync.Mutex
	ring    []streamItem
	seq     int64
	clients map[chan streamItem]struct{}
	done    chan struct{}
	closed  sync.Once
	pending map[string]struct{}
}

// streamItem is one SSE payload with its sequence number. The payload is a
// marshalled event or ui_request; the transport does not interpret it.
type streamItem struct {
	seq     int64
	payload []byte
}

// Handler builds the HTTP handler over s and starts its event pump.
func Handler(s *mode.Server, opt Options) (http.Handler, error) {
	srv, err := newServer(s, opt)
	if err != nil {
		return nil, err
	}
	srv.start()
	return srv, nil
}

// newServer validates options and wires the routes without starting the pump.
func newServer(s *mode.Server, opt Options) (*server, error) {
	if s == nil {
		return nil, fmt.Errorf("serve: mode.Server is required")
	}
	if err := opt.apply(); err != nil {
		return nil, err
	}
	srv := &server{mode: s, opt: opt, clients: map[chan streamItem]struct{}{}, done: make(chan struct{}), pending: map[string]struct{}{}}
	srv.mux = http.NewServeMux()
	srv.mux.HandleFunc("POST /v1/commands", srv.handleCommands)
	srv.mux.HandleFunc("POST /v1/attachments", srv.handleAttachments)
	srv.mux.HandleFunc("GET /v1/events", srv.handleEvents)
	srv.mux.HandleFunc("POST /v1/ui_response", srv.handleUIResponse)
	srv.mux.HandleFunc("GET /v1/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.Handle(r.Context(), proto.Command{Type: proto.CmdGetState}))
	})
	srv.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	s.SetUIHandler(srv.forwardUI)
	return srv, nil
}

// start pumps server events into the ring until close.
func (s *server) start() {
	events, cancel := s.mode.Subscribe(4096)
	go func() {
		defer cancel()
		defer diag.Catch("server events")
		for {
			select {
			case <-s.done:
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
	defer func() {
		if recover() != nil {
			writeJSON(w, http.StatusInternalServerError, fail(proto.CodeInternal, "internal error"))
		}
	}()
	// The event stream is long-lived by design, so only the request/response routes
	// get a write deadline, set before auth so that rejections are bounded too; a stalled reader cannot pin a handler forever. A writer
	// without deadline support (a test recorder) returns an error we can ignore.
	if r.URL.Path != "/v1/events" {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeTimeout))
	}
	if s.opt.CORSOrigin != "" {
		w.Header().Add("Vary", "Origin")
		if r.Header.Get("Origin") == s.opt.CORSOrigin {
			w.Header().Set("Access-Control-Allow-Origin", s.opt.CORSOrigin)
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || !strings.EqualFold(auth[:7], "Bearer ") {
		auth = ""
	} else {
		auth = auth[7:]
	}
	if r.URL.Path != "/healthz" && s.opt.Token != "" && !constantTimeEqual(auth, s.opt.Token) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSON(w, http.StatusUnauthorized, fail("unauthorized", ""))
		return
	}
	if (s.opt.Token == "" || len(s.opt.AllowedHosts) > 0) && r.Method != http.MethodOptions && !hostAllowed(r.Host, s.opt.AllowedHosts) {
		writeJSON(w, http.StatusForbidden, fail("forbidden", ""))
		return
	}
	s.mux.ServeHTTP(w, r)
}

// fail builds a response-shaped error body.
func fail(code, message string) proto.Response {
	return proto.Response{Type: proto.FrameResponse, Error: &proto.Error{Code: code, Message: message}}
}

// readBody enforces the content type and body limit and returns the bytes. On
// failure it has already written the response.
func (s *server) readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	if strings.TrimSpace(r.Header.Get("Content-Type")) != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, fail(proto.CodeUnsupported, "Content-Type must be application/json"))
		return nil, false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		if maxErr := (*http.MaxBytesError)(nil); errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, fail(proto.CodeBadRequest, "request body too large"))
		} else {
			writeJSON(w, http.StatusBadRequest, fail(proto.CodeBadRequest, "invalid request body"))
		}
		return nil, false
	}
	return data, true
}

// handleCommands takes one command frame and returns its response frame.
func (s *server) handleCommands(w http.ResponseWriter, r *http.Request) {
	data, ok := s.readBody(w, r, s.opt.MaxBody)
	if !ok {
		return
	}
	var cmd proto.Command
	if err := json.Unmarshal(data, &cmd); err != nil {
		writeJSON(w, http.StatusBadRequest, fail(proto.CodeBadRequest, "invalid JSON"))
		return
	}
	writeJSON(w, http.StatusOK, s.mode.Handle(r.Context(), cmd))
}

// handleAttachments accepts one image's raw bytes and returns an id a later
// command can reference in an Attachment. It is the two-step upload a browser
// or another client uses; the inline data form still works. The bytes are
// validated and bounded by the server's store, not by this transport.
func (s *server) handleAttachments(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.opt.MaxBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, fail(proto.CodeBadRequest, "request body too large"))
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.Header.Get("X-Attachment-Name")
	}
	mime := r.Header.Get("Content-Type")
	id, err := s.mode.StoreAttachment(name, mime, data)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, fail(proto.CodeBadRequest, err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "mime_type": mime, "size_bytes": len(data)})
}

// handleUIResponse routes an answer to the request that is waiting for it.
func (s *server) handleUIResponse(w http.ResponseWriter, r *http.Request) {
	data, ok := s.readBody(w, r, 1<<20)
	if !ok {
		return
	}
	var resp proto.UIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		writeJSON(w, http.StatusBadRequest, fail(proto.CodeBadRequest, "invalid JSON"))
		return
	}
	s.mu.Lock()
	_, live := s.pending[resp.ID]
	delete(s.pending, resp.ID)
	s.mu.Unlock()
	if !live {
		writeJSON(w, http.StatusNotFound, fail(proto.CodeNotFound, "unknown ui_request id"))
		return
	}
	s.mode.AnswerUI(resp)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// forwardUI streams a UI request to the event clients and marks its id live.
// The answer arrives later as a separate POST /v1/ui_response.
func (s *server) forwardUI(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	s.mu.Lock()
	s.pending[req.ID] = struct{}{}
	s.mu.Unlock()
	s.publish(req)
	return proto.UIResponse{}, nil
}

// publish assigns the next sequence number, appends the payload to the ring,
// and fans it out. A client whose buffer is full is dropped, never blocked.
func (s *server) publish(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	it := streamItem{seq: s.seq, payload: data}
	s.ring = append(s.ring, it)
	if len(s.ring) > s.opt.RingSize {
		s.ring = append([]streamItem(nil), s.ring[len(s.ring)-s.opt.RingSize:]...)
	}
	for ch := range s.clients {
		select {
		case ch <- it:
		default:
			delete(s.clients, ch)
			close(ch)
		}
	}
}

// subscribe registers a client and returns the events it must replay. With no
// Last-Event-ID (hasLast false) it starts live. If lastID is older than the
// ring, resync is true and the caller sends a state snapshot instead.
func (s *server) subscribe(lastID int64, hasLast bool) (replay []streamItem, ch chan streamItem, resync bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch = make(chan streamItem, 256)
	s.clients[ch] = struct{}{}
	if !hasLast {
		return nil, ch, false
	}
	if len(s.ring) > 0 && lastID+1 < s.ring[0].seq {
		return nil, ch, true
	}
	for _, it := range s.ring {
		if it.seq > lastID {
			replay = append(replay, it)
		}
	}
	return replay, ch, false
}

// unsubscribe removes a client. It is safe when the publisher dropped it first.
func (s *server) unsubscribe(ch chan streamItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[ch]; ok {
		delete(s.clients, ch)
		close(ch)
	}
}

// handleEvents streams the server's events as server-sent events.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, fail(proto.CodeInternal, "streaming unsupported"))
		return
	}
	lastID, hasLast := int64(0), false
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			lastID, hasLast = n, true
		}
	}
	// Subscribe before the headers go out: a client that has seen the 200 may
	// send a command at once, and its events must not be published to nobody.
	replay, ch, resync := s.subscribe(lastID, hasLast)
	defer s.unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var skip int64
	if resync {
		if state := s.mode.Handle(r.Context(), proto.Command{Type: proto.CmdGetState}); state.OK {
			if data, err := json.Marshal(proto.Event{Type: proto.EventStateChanged, State: state.Data, Reason: "resync"}); err == nil {
				s.mu.Lock()
				s.seq++
				skip = s.seq
				s.mu.Unlock()
				fmt.Fprintf(w, "id: %d\ndata: %s\n\n", skip, data)
				flusher.Flush()
			}
		}
	}
	for _, it := range replay {
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", it.seq, it.payload)
		flusher.Flush()
	}

	heartbeat := time.After(s.opt.Heartbeat)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			_, _ = io.WriteString(w, ": closing\n\n")
			flusher.Flush()
			return
		case <-heartbeat:
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
			heartbeat = time.After(s.opt.Heartbeat)
		case it, ok := <-ch:
			if !ok {
				return
			}
			if it.seq <= skip {
				continue
			}
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", it.seq, it.payload)
			flusher.Flush()
		}
	}
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
	httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: srv.opt.headerTimeout, IdleTimeout: idleTimeout}
	srv.start()
	errCh := make(chan error, 1)
	go func() { defer diag.Catch("http server"); errCh <- httpSrv.Serve(ln) }()
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

// constantTimeEqual compares the presented token with the configured one
// without an early exit on the first differing byte. It is a named wrapper so a
// test can prove that the token check goes through subtle.ConstantTimeCompare.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// loopbackListen reports whether a listen address or Host value names only the
// loopback interface. An empty host, a wildcard address, or a hostname is not
// loopback; the port, when present, never matters.
func loopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hostAllowed reports whether the Host header is trusted.
func hostAllowed(header string, allowed []string) bool {
	if loopbackListen(header) {
		return true
	}
	host, _, err := net.SplitHostPort(header)
	if err != nil {
		host = header
	}
	for _, a := range allowed {
		if strings.EqualFold(a, host) || strings.EqualFold(a, header) {
			return true
		}
	}
	return false
}

// writeJSON writes v as a JSON response with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
