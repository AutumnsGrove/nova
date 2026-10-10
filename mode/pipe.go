package mode

import (
	"context"

	"github.com/AutumnsGrove/nova/proto"
)

// Client is an in-process connection to a Server, used by the TUI and tests.
// Its capabilities are the UI methods it can answer; the server will not send
// it a request it did not declare.
type Client struct {
	s        *Server
	events   <-chan proto.Event
	cancel   func()
	requests chan proto.UIRequest
	answers  chan proto.UIResponse
}

// NewPipe attaches a client to a server. capabilities are the ui_request
// methods the client can answer (for example "ui.confirm").
func NewPipe(s *Server, capabilities ...string) *Client {
	events, cancel := s.Subscribe(0)
	c := &Client{
		s:        s,
		events:   events,
		cancel:   cancel,
		requests: make(chan proto.UIRequest, 8),
		answers:  make(chan proto.UIResponse, 8),
	}
	caps := map[string]bool{}
	for _, cap := range capabilities {
		caps[cap] = true
	}
	s.uiMu.Lock()
	s.uiCaps = caps
	s.uiHandler = c.serveUI
	s.uiMu.Unlock()
	return c
}

// serveUI hands a request to the client and waits for its answer. Fire-and-
// forget methods are delivered without waiting for one.
func (c *Client) serveUI(ctx context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	select {
	case c.requests <- req:
	case <-ctx.Done():
		return proto.UIResponse{}, ctx.Err()
	}
	if fireAndForgetMethod(req.Method) {
		return proto.UIResponse{Type: proto.FrameUIResponse, ID: req.ID}, nil
	}
	select {
	case resp := <-c.answers:
		resp.ID = req.ID
		return resp, nil
	case <-ctx.Done():
		return proto.UIResponse{}, ctx.Err()
	}
}

// fireAndForgetMethod reports a UI method that needs no answer.
func fireAndForgetMethod(method string) bool {
	switch method {
	case "notify", "status", "widget", "title", "set_editor_text":
		return true
	}
	return false
}

// Events is the client's event stream.
func (c *Client) Events() <-chan proto.Event { return c.events }

// Requests is the stream of ui_requests the server wants answered.
func (c *Client) Requests() <-chan proto.UIRequest { return c.requests }

// Answer answers the outstanding ui_request.
func (c *Client) Answer(resp proto.UIResponse) { c.answers <- resp }

// Send handles one command.
func (c *Client) Send(ctx context.Context, cmd proto.Command) proto.Response {
	return c.s.Handle(ctx, cmd)
}

// Close detaches the client.
func (c *Client) Close() { c.cancel() }

// supportsUI reports whether a client can answer the given method.
func (s *Server) supportsUI(method string) bool {
	s.uiMu.Lock()
	defer s.uiMu.Unlock()
	if s.uiHandler == nil {
		return false
	}
	if s.uiCaps == nil {
		return true
	}
	return s.uiCaps[method]
}
