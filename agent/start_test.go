package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

// startHung starts a run against a provider that blocks until cancelled.
func startHung(t *testing.T) (*Agent, *collector) {
	t.Helper()
	c := &collector{}
	cfg := Config{Provider: testkit.NewProvider(testkit.RealClock(), testkit.Turn{Hang: true}), Model: "m", Emit: c.emit}
	return New(cfg), c
}

func waitIdle(t *testing.T, a *Agent) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for a.Running() {
		if time.Now().After(deadline) {
			t.Fatal("run did not end after Abort")
		}
		time.Sleep(time.Millisecond)
	}
}

func Test_STA01_StartRegistersBeforeReturning(t *testing.T) {
	a, _ := startHung(t)
	if err := a.Start(context.Background(), nil, message.User("go")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The run is registered synchronously, so there is no window in which a
	// caller sees an idle agent right after Start returned.
	if !a.Running() {
		t.Fatal("Running() = false immediately after Start")
	}
	a.Abort()
	waitIdle(t, a)
}

func Test_STA02_AbortRightAfterStartAlwaysLands(t *testing.T) {
	for i := 0; i < 200; i++ {
		a, c := startHung(t)
		if err := a.Start(context.Background(), nil, message.User("go")); err != nil {
			t.Fatalf("iteration %d: Start: %v", i, err)
		}
		a.Abort()
		waitIdle(t, a)
		ends := 0
		for _, e := range c.all() {
			if e.Type == proto.EventRunEnd {
				ends++
				if e.Reason != "aborted" {
					t.Fatalf("iteration %d: run_end reason = %q, want aborted", i, e.Reason)
				}
			}
		}
		if ends != 1 {
			t.Fatalf("iteration %d: %d run_end events, want 1", i, ends)
		}
	}
}

func Test_STA03_StartWhileRunningIsBusy(t *testing.T) {
	a, _ := startHung(t)
	if err := a.Start(context.Background(), nil, message.User("one")); err != nil {
		t.Fatal(err)
	}
	defer func() { a.Abort(); waitIdle(t, a) }()
	if err := a.Start(context.Background(), nil, message.User("two")); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start = %v, want ErrBusy", err)
	}
}

func Test_STA04_StartRejectsBadConfigSynchronously(t *testing.T) {
	a := New(Config{Provider: nil, Model: "m"})
	if err := a.Start(context.Background(), nil, message.User("go")); err == nil {
		t.Fatal("Start with no provider returned nil")
	}
	if a.Running() {
		t.Fatal("a rejected Start left the agent running")
	}
}
