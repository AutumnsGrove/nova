package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// A reload changes limits that were fixed when the agent was built. The next
// run must use them.
func Test_LPX01_ReconfigureAppliesToTheNextRun(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, alwaysTool(8)...)
	a := New(Config{Provider: p, Model: "m", Clock: clock, MaxTurns: 1, Tools: registry(echoTool())})
	res, err := a.Run(context.Background(), nil, message.User("go"))
	if err != nil || res.Turns != 1 || !res.Exhausted {
		t.Fatalf("first run = %+v, %v, want exhausted after 1 turn", res, err)
	}
	if err := a.Reconfigure(func(c *Config) { c.MaxTurns = 3 }); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	res, err = a.Run(context.Background(), nil, message.User("again"))
	if err != nil || res.Turns != 3 || !res.Exhausted {
		t.Fatalf("second run = %+v, %v, want exhausted after 3 turns", res, err)
	}
}

// A run keeps the configuration it began with, so changing it mid-run is refused
// rather than half applied.
func Test_LPX02_ReconfigureIsRefusedDuringARun(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	started, release := make(chan struct{}), make(chan struct{})
	block := toolOf("echo", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		close(started)
		<-release
		return tool.Text("done"), nil
	})
	p := testkit.NewProvider(clock, toolTurn(), textTurn("end"))
	a := New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(block)})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = a.Run(context.Background(), nil, message.User("go"))
	}()
	<-started
	called := false
	if err := a.Reconfigure(func(*Config) { called = true }); !errors.Is(err, ErrBusy) || called {
		t.Fatalf("Reconfigure during a run = %v (callback ran: %v), want ErrBusy and no callback", err, called)
	}
	close(release)
	<-done
}
