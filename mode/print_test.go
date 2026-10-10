package mode

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

func TestPrint_PRI08_SIGINT(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	events, cancelSub := s.Subscribe(64)
	defer cancelSub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- RunPrint(ctx, s, "hi", &out, &errOut) }()
	waitFor(t, events, proto.EventRunStart)
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("exit = %d, want 130", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunPrint did not return after cancellation")
	}
}
