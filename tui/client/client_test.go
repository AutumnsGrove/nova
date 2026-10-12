//go:build !notui

package client

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
)

// fakePipe is an in-memory pipe for tests.
type fakePipe struct {
	sent     []proto.Command
	resp     proto.Response
	events   chan proto.Event
	requests chan proto.UIRequest
	answered []proto.UIResponse
}

func newFakePipe() *fakePipe {
	return &fakePipe{
		events:   make(chan proto.Event, 16),
		requests: make(chan proto.UIRequest, 16),
	}
}

func (f *fakePipe) Send(_ context.Context, cmd proto.Command) proto.Response {
	f.sent = append(f.sent, cmd)
	return f.resp
}
func (f *fakePipe) Events() <-chan proto.Event       { return f.events }
func (f *fakePipe) Requests() <-chan proto.UIRequest { return f.requests }
func (f *fakePipe) Answer(resp proto.UIResponse)     { f.answered = append(f.answered, resp) }

func TestClient_CLT01_SendYieldsResponse(t *testing.T) {
	f := newFakePipe()
	f.resp = proto.Response{Type: proto.FrameResponse, Command: proto.CmdGetState, OK: true}
	c := &pipeClient{p: f}
	cmd := proto.Command{Type: proto.CmdGetState}
	msg, ok := c.Send(cmd)().(ResponseMsg)
	if !ok {
		t.Fatalf("Send returned %T, want ResponseMsg", c.Send(cmd)())
	}
	if !msg.OK {
		t.Fatal("response is not OK")
	}
	if len(f.sent) != 1 || f.sent[0].Type != proto.CmdGetState {
		t.Fatalf("pipe did not receive the command: %+v", f.sent)
	}
}

func TestClient_CLT02_NextYieldsEventsInOrder(t *testing.T) {
	f := newFakePipe()
	f.events <- proto.Event{Type: proto.EventRunStart, Seq: 1}
	f.events <- proto.Event{Type: proto.EventRunEnd, Seq: 2}
	c := &pipeClient{p: f}
	first, ok := c.Next()().(EventMsg)
	if !ok || first.Seq != 1 {
		t.Fatalf("first event = %#v", first)
	}
	second, ok := c.Next()().(EventMsg)
	if !ok || second.Seq != 2 {
		t.Fatalf("second event = %#v", second)
	}
}

func TestClient_CLT03_PipeClosed(t *testing.T) {
	f := newFakePipe()
	close(f.events)
	close(f.requests)
	c := &pipeClient{p: f}
	if _, ok := c.Next()().(ClosedMsg); !ok {
		t.Fatalf("closed pipe returned %T, want ClosedMsg", c.Next()())
	}
}

func TestClient_CLT04_UIRequestDeliveredAndAnswered(t *testing.T) {
	f := newFakePipe()
	f.requests <- proto.UIRequest{Type: proto.FrameUIRequest, ID: "u1", Method: "confirm"}
	c := &pipeClient{p: f}
	got, ok := c.Next()().(UIRequestMsg)
	if !ok {
		t.Fatalf("Next returned %T, want UIRequestMsg", got)
	}
	if got.ID != "u1" || got.Method != "confirm" {
		t.Fatalf("wrong UI request: %+v", got)
	}
	resp := proto.UIResponse{Type: proto.FrameUIResponse, ID: "u1", OK: true}
	var _ tea.Cmd = c.AnswerUI(resp)
	c.AnswerUI(resp)()
	if len(f.answered) == 0 || f.answered[0].ID != "u1" {
		t.Fatalf("UI request was not answered: %+v", f.answered)
	}
}
