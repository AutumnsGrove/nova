package ext

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/testkit"
)

// stalledConn builds a connection whose peer never reads, so its writer blocks
// on the first frame and every later one queues behind it.
func stalledConn(t *testing.T, clock Clock) *conn {
	t.Helper()
	testkit.NoLeaks(t)
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	c := newConn("stalled", stdoutR, stdinW, closerFunc(func() error {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutW.Close()
		return stdoutR.Close()
	}), clock, nil)
	t.Cleanup(func() { c.close() })
	return c
}

// closerFunc adapts a function to io.Closer.
type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// A peer that stops reading its stdin must not hold a caller past the
// request's own deadline, or a wedged extension would hang the agent loop.
func TestConn_RequestHonoursDeadlineWhenPeerStopsReading(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	c := stalledConn(t, clk)
	// One frame is stuck in the writer and the queue holds the rest.
	for i := 0; i < cap(c.writeCh)+1; i++ {
		if err := c.notify(&outFrame{Type: "fill"}); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.request(context.Background(), &outFrame{Type: "tool_call"}, time.Second)
		done <- err
	}()
	watchdog := time.After(5 * time.Second)
	for {
		select {
		case err := <-done:
			if !errors.Is(err, errDeadline) {
				t.Fatalf("err = %v, want %v", err, errDeadline)
			}
			return
		case <-watchdog:
			t.Fatal("request hung past its deadline with a full write queue")
		default:
			clk.Advance(time.Second)
			time.Sleep(time.Millisecond)
		}
	}
}

// Cancelling the caller's context must also free a request stuck behind a
// full write queue.
func TestConn_RequestHonoursCancelWhenPeerStopsReading(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	c := stalledConn(t, clk)
	for i := 0; i < cap(c.writeCh)+1; i++ {
		if err := c.notify(&outFrame{Type: "fill"}); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.request(ctx, &outFrame{Type: "tool_call"}, time.Hour)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, errCancelled) {
			t.Fatalf("err = %v, want %v", err, errCancelled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request hung after its context was cancelled")
	}
}
