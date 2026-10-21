package mode

import (
	"context"
	"strconv"
	"testing"
	"time"

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

// A fire-and-forget request must reach the client, and in the order it was
// sent. The extension host calls Notify and moves on, so a dropped or reordered
// widget or status update is never corrected and the UI would show stale state.
func TestPipe_SRV54_NotifyDeliveredInOrder(t *testing.T) {
	s, _ := testServer(t)
	c := NewPipe(s, "ui.status")
	defer c.Close()
	const n = notifyQueueSize / 2
	for i := range n {
		uiBridge{s}.Notify(proto.UIRequest{Method: "status", Key: strconv.Itoa(i)})
	}
	for want := range n {
		select {
		case req := <-c.Requests():
			if req.Key != strconv.Itoa(want) {
				t.Fatalf("notification %d arrived as %q", want, req.Key)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d notifications arrived", want, n)
		}
	}
}

// A client that never reads must not stall the extension that notifies it.
func TestPipe_SRV55_NotifyNeverBlocks(t *testing.T) {
	s, _ := testServer(t)
	c := NewPipe(s, "ui.status")
	defer c.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 4 * notifyQueueSize {
			uiBridge{s}.Notify(proto.UIRequest{Method: "status", Key: "k"})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Notify blocked on a client that is not reading")
	}
}
