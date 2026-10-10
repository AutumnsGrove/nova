package mode

import (
	"context"
	"testing"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/proto"
)

func TestPipe_SRV44_Capabilities(t *testing.T) {
	s, _ := testServer(t)

	noUI := NewPipe(s)
	allow, err := s.ask(context.Background(), agent.Question{Tool: "write"})
	if err != nil || allow {
		t.Fatalf("ask = %v, %v; a client without ui.confirm must fall back to headless", allow, err)
	}
	noUI.Close()

	c := NewPipe(s, "ui.confirm")
	defer c.Close()
	done := make(chan bool, 1)
	go func() {
		a, _ := s.ask(context.Background(), agent.Question{Tool: "write"})
		done <- a
	}()
	req := <-c.Requests()
	if req.Method != "ui.confirm" {
		t.Fatalf("method = %q, want ui.confirm", req.Method)
	}
	c.Answer(proto.UIResponse{Type: proto.FrameUIResponse, OK: true})
	if !<-done {
		t.Fatal("the client's yes should allow the call")
	}
}
