//go:build !notui

package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/client"
	"github.com/AutumnsGrove/nova/tui/splash"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// recordClock records every requested timer period and yields tickMsg.
type recordClock struct {
	now  time.Time
	durs []time.Duration
}

func (c *recordClock) Now() time.Time { return c.now }
func (c *recordClock) After(d time.Duration) tea.Cmd {
	c.durs = append(c.durs, d)
	return func() tea.Msg { return tickMsg{} }
}

// splashModel builds a truecolor model large enough for the Full tier.
func splashModel(t *testing.T, w, h int) (*Model, *recordClock) {
	t.Helper()
	clk := &recordClock{now: time.Unix(1_700_000_000, 0)}
	m := New(Options{
		Client: &fakeClient{responses: map[string]proto.Response{}},
		Theme:  styles.Dark(), Clock: clk, CWD: t.TempDir(),
		Profile: colorprofile.TrueColor, Renderer: echoRenderer{},
		Width: w, Height: h,
	})
	return m, clk
}

func TestUI_SQN33_SplashTicksWhileVisible(t *testing.T) {
	m, clk := splashModel(t, 120, 60)
	if cmd := m.scheduleTick(); cmd == nil {
		t.Fatal("no tick scheduled for the splash")
	}
	if len(clk.durs) != 1 || clk.durs[0] != splashTick {
		t.Fatalf("tick period = %v, want %v", clk.durs, splashTick)
	}
	update(m, tickMsg{})
	if len(clk.durs) != 2 {
		t.Fatalf("no follow-up tick after tickMsg: %v", clk.durs)
	}
}

func TestUI_SQN34_ElapsedFollowsClock(t *testing.T) {
	m, clk := splashModel(t, 120, 60)
	m.scheduleTick()
	clk.now = clk.now.Add(2 * time.Second)
	update(m, tickMsg{})
	if m.splashAt != 2*time.Second {
		t.Fatalf("splashAt = %v, want 2s", m.splashAt)
	}
	want, _, ok := splash.Render(m.layout.Main.W, m.layout.Main.H, m.st, m.profile, m.version, 2*time.Second)
	if !ok {
		t.Fatal("reference render not ok")
	}
	if got := m.mainView(); got != want {
		t.Fatalf("mainView does not match Render at 2s")
	}
}

func TestUI_SQN35_DismissalStopsTicks(t *testing.T) {
	m, clk := splashModel(t, 120, 60)
	m.scheduleTick()
	update(m, runeKey('a'))
	if m.showSplash() {
		t.Fatal("splash not dismissed by a key")
	}
	update(m, tickMsg{})
	if m.ticking {
		t.Fatal("a tick is still pending after dismissal")
	}
	if len(clk.durs) != 1 {
		t.Fatalf("unexpected extra ticks: %v", clk.durs)
	}
}

func TestUI_SQN36_FirstEventStopsTicks(t *testing.T) {
	m, clk := splashModel(t, 120, 60)
	m.scheduleTick()
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
	if m.showSplash() {
		t.Fatal("splash not dismissed by an event")
	}
	before := m.splashAt
	clk.now = clk.now.Add(time.Second)
	update(m, tickMsg{})
	if m.splashAt != before {
		t.Fatalf("splashAt advanced after dismissal: %v", m.splashAt)
	}
}

func TestUI_SQN37_NoTickWhenOffOrNotAnimated(t *testing.T) {
	m, _ := splashModel(t, 120, 60)
	m.display.Splash = false
	m.ticking = false
	if m.scheduleTick() != nil {
		t.Fatal("tick scheduled with the splash off")
	}

	plain, _, _ := newModel(t)
	plain.ticking = false
	if plain.scheduleTick() != nil {
		t.Fatal("tick scheduled for a plain tier")
	}

	nonEmpty, _ := splashModel(t, 120, 60)
	nonEmpty.transcript.AddUser("hi", nil)
	nonEmpty.syncList()
	nonEmpty.ticking = false
	if nonEmpty.scheduleTick() != nil {
		t.Fatal("tick scheduled with a non-empty transcript")
	}
}

func TestUI_SQN38_OneTimerChain(t *testing.T) {
	m, _ := splashModel(t, 120, 60)
	m.ticking = false
	if m.scheduleTick() == nil {
		t.Fatal("no tick for the splash")
	}
	if m.scheduleTick() != nil {
		t.Fatal("a second splash tick was scheduled")
	}

	m.splashOff = true
	m.ticking = false
	m.transcript.Apply(proto.Event{Type: proto.EventThinkingStart, Run: "r", Turn: intp(1)})
	if m.scheduleTick() == nil {
		t.Fatal("no tick for a thinking block")
	}
	if m.scheduleTick() != nil {
		t.Fatal("a second thinking tick was scheduled")
	}
}

func TestUI_SQN39_ResizeKeepsTime(t *testing.T) {
	m, clk := splashModel(t, 120, 60)
	m.scheduleTick()
	clk.now = clk.now.Add(3 * time.Second)
	update(m, tickMsg{})
	if m.splashAt != 3*time.Second {
		t.Fatalf("splashAt = %v, want 3s", m.splashAt)
	}
	update(m, tea.WindowSizeMsg{Width: 70, Height: 30})
	if m.splashAt != 3*time.Second {
		t.Fatalf("resize changed splashAt to %v", m.splashAt)
	}
	_, tier, ok := splash.Render(m.layout.Main.W, m.layout.Main.H, m.st, m.profile, m.version, m.splashAt)
	if !ok || tier != splash.Compact {
		t.Fatalf("tier after resize = %v ok=%v, want Compact", tier, ok)
	}
}
