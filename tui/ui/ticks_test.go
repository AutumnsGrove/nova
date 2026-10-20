//go:build !notui

package ui

import (
	"testing"
	"time"
)

func TestUI_KEY15_QuitTimerDoesNotForkTheTickChain(t *testing.T) {
	m, clk := splashModel(t, 80, 24)
	m.splashDone = true // leave only the status clock animating
	m.recompute()
	if m.scheduleTick() == nil {
		t.Fatal("the session timer should keep a tick chain running")
	}
	cmd := update(m, ctrlKey('c')) // arms the quit window with a timer of its own
	if !m.quitArmed || cmd == nil {
		t.Fatalf("ctrl+c did not arm the quit window: armed=%v cmd=%v", m.quitArmed, cmd)
	}
	requested := len(clk.durs)
	clk.now = clk.now.Add(quitWindow + time.Second)
	update(m, do(cmd)) // the quit timer fires
	if extra := len(clk.durs) - requested; extra != 0 {
		t.Fatalf("the quit timer's message started %d more tick timer(s); the chain forks and ticks twice as often", extra)
	}
	if m.quitArmed {
		t.Fatal("the quit window did not expire when its timer fired")
	}
}
