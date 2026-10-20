//go:build !notui

package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/tui/chat"
	"github.com/AutumnsGrove/nova/tui/styles"
)

func TestUI_KEY16_HintFollowsTheSavedDisplay(t *testing.T) {
	display := chat.DefaultDisplay()
	display.ExpandTools = true
	m := New(Options{
		Client: &fakeClient{}, Theme: styles.Mono(), Clock: &fakeClock{now: time.Unix(1_700_000_000, 0)},
		CWD: t.TempDir(), Profile: colorprofile.ASCII, Renderer: echoRenderer{}, Display: display,
	})
	if got := m.hint(); got != "ctrl+o collapse" {
		t.Fatalf("hint = %q; blocks start expanded, so ctrl+o collapses them", got)
	}
}

func TestUI_UXB09_NoticesAreShown(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("/nosuchcommand")
	m.commands = []commandInfo{{Name: "model"}}
	update(m, enterKey)
	if v := m.render(); !strings.Contains(v, "unknown command: /nosuchcommand") {
		t.Fatalf("the notice never reached the screen:\n%s", v)
	}
}

func TestUI_KEY17_ExpandAllMakesTheFlagsUniform(t *testing.T) {
	m, _, _ := newModel(t)
	m.display.ExpandThinking, m.display.ExpandTools = true, false
	update(m, ctrlKey('o'))
	if !m.display.ExpandThinking || !m.display.ExpandTools {
		t.Fatalf("ctrl+o left thinking=%v tools=%v; it should expand every block",
			m.display.ExpandThinking, m.display.ExpandTools)
	}
	update(m, ctrlKey('o'))
	if m.display.ExpandThinking || m.display.ExpandTools {
		t.Fatalf("the second ctrl+o left thinking=%v tools=%v", m.display.ExpandThinking, m.display.ExpandTools)
	}
}

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
