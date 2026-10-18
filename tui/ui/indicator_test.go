//go:build !notui

package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/client"
)

// IND01: the footer shows a run indicator for the whole of a run and clears it
// the moment run_end arrives.
func TestUI_IND01_ShowsWhileRunning(t *testing.T) {
	m, _, _ := newModel(t)
	if strings.Contains(m.footer(), "working") {
		t.Fatalf("indicator shown before a run: %q", m.footer())
	}
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
	if got := m.footer(); !strings.Contains(got, "working") {
		t.Fatalf("no run indicator in the footer: %q", got)
	}
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunEnd}})
	if got := m.footer(); strings.Contains(got, "working") {
		t.Fatalf("indicator survived run_end: %q", got)
	}
}

// IND02: the indicator animates and carries the elapsed run time.
func TestUI_IND02_AnimatesAndTimes(t *testing.T) {
	m, _, clk := newModel(t)
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
	first := m.footer()
	clk.now = clk.now.Add(400 * time.Millisecond)
	second := m.footer()
	if first == second {
		t.Fatalf("indicator did not change with the clock: %q", first)
	}
	if !strings.Contains(second, "400ms") {
		t.Fatalf("elapsed time missing from the footer: %q", second)
	}
}

// IND03: a run whose only activity is waiting on the provider still shows the
// indicator and keeps the tick chain alive.
func TestUI_IND03_WaitingOnlyRun(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
	if !strings.Contains(m.footer(), "working") {
		t.Fatalf("no indicator while waiting on the provider: %q", m.footer())
	}
	if !m.needsTick() {
		t.Fatal("needsTick() is false for a bare run")
	}
}

// IND04: run_end stops the tick chain, so no timer is left running.
func TestUI_IND04_TicksStopOnEnd(t *testing.T) {
	m, clk := splashModel(t, 120, 60)
	m.display.Splash = false
	m.recompute()
	clk.durs = nil
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
	if len(clk.durs) != 1 {
		t.Fatalf("run_start scheduled %d timers, want 1: %v", len(clk.durs), clk.durs)
	}
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunEnd}})
	update(m, tickMsg{})
	if len(clk.durs) != 1 {
		t.Fatalf("a tick was rescheduled after run_end: %v", clk.durs)
	}
	if m.ticking {
		t.Fatal("ticking is still true after run_end")
	}
}

// IND06: the footer indicator at three widths, under NO_COLOR (Mono).
func TestUI_IND06_Golden(t *testing.T) {
	golden.RequireEqual(t, goldAt(t, func(m *Model) {
		update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
		if f, ok := m.clock.(*fakeClock); ok {
			f.now = f.now.Add(3 * time.Second)
		}
	}))
}

// IND05: state_changed running=true starts the indicator too, for a run this
// client did not see start.
func TestUI_IND05_StateChangedStarts(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventStateChanged, State: []byte(`{"running":true}`)}})
	if !strings.Contains(m.footer(), "working") {
		t.Fatalf("state_changed running=true did not show the indicator: %q", m.footer())
	}
}
