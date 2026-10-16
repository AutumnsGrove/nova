package mode

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
)

// ask resolves a permission question. With a UI handler it sends a confirm
// request; without one it applies the policy's headless action. A tool the
// user allowed for the session is approved without asking again.
func (s *Server) ask(ctx context.Context, q agent.Question) (bool, error) {
	if s.sessionAllowed(q.Tool) {
		return true, nil
	}
	if !s.supportsUI("ui.confirm") {
		return s.policy.Headless == permission.Allow, nil
	}
	allowByDefault := s.policy.Headless == permission.Allow
	req := proto.UIRequest{
		Type:     proto.FrameUIRequest,
		ID:       s.newUIID(),
		Method:   "ui.confirm",
		Title:    "Allow " + q.Tool + "?",
		Message:  q.Reason,
		Default:  &allowByDefault,
		Remember: true,
		Tool:     q.Tool,
		Args:     q.Args,
	}
	resp, err := s.callUI(ctx, req)
	if err != nil {
		return false, err
	}
	if resp.OK && resp.Remember == "session" {
		s.allowSession(q.Tool)
	}
	return resp.OK, nil
}

// sessionAllowed reports whether the user allowed tool for this server.
func (s *Server) sessionAllowed(tool string) bool {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	return s.sessionAllow[tool]
}

// allowSession records a session-scoped allow rule for tool.
func (s *Server) allowSession(tool string) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.sessionAllow == nil {
		s.sessionAllow = map[string]bool{}
	}
	s.sessionAllow[tool] = true
}

// callUI delivers a UI request and waits for its answer. The handler may answer
// synchronously; a transport that answers with a separate ui_response frame
// leaves the handler's response empty and AnswerUI delivers instead.
func (s *Server) callUI(ctx context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	ch := make(chan proto.UIResponse, 1)
	s.uiMu.Lock()
	s.uiPending[req.ID] = ch
	handler := s.uiHandler
	s.uiMu.Unlock()
	defer func() {
		s.uiMu.Lock()
		delete(s.uiPending, req.ID)
		s.uiMu.Unlock()
	}()

	if handler != nil {
		go func() {
			defer diag.Catch("ui handler")
			resp, err := handler(ctx, req)
			if err != nil || !answered(resp) {
				return
			}
			select {
			case ch <- resp:
			default:
			}
		}()
	}

	var timeout <-chan time.Time
	if req.TimeoutMS != nil && *req.TimeoutMS >= 0 {
		timer := time.NewTimer(time.Duration(*req.TimeoutMS) * time.Millisecond)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-timeout:
		s.emit(proto.Event{Type: proto.EventUIExpired, ID: req.ID})
		return defaultUIResponse(req), nil
	case <-ctx.Done():
		return proto.UIResponse{}, ctx.Err()
	}
}

// answered reports whether a handler response carries an actual answer.
func answered(resp proto.UIResponse) bool {
	return resp.ID != "" || resp.OK || resp.Cancelled || len(resp.IDs) > 0 || resp.Text != ""
}

// defaultUIResponse builds the answer used when a request times out.
func defaultUIResponse(req proto.UIRequest) proto.UIResponse {
	resp := proto.UIResponse{Type: proto.FrameUIResponse, ID: req.ID}
	if req.Default != nil {
		resp.OK = *req.Default
	}
	return resp
}

// AnswerUI routes a client response to the request that is waiting for it. An
// unknown or expired id is ignored with a log line.
func (s *Server) AnswerUI(resp proto.UIResponse) {
	s.uiMu.Lock()
	ch := s.uiPending[resp.ID]
	s.uiMu.Unlock()
	if ch == nil {
		fmt.Fprintf(os.Stderr, "nova: ignoring ui_response for unknown or expired id %q\n", resp.ID)
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

// newUIID returns the next ui_request id.
func (s *Server) newUIID() string {
	s.uiMu.Lock()
	defer s.uiMu.Unlock()
	s.uiNext++
	return fmt.Sprintf("ui_%d", s.uiNext)
}
