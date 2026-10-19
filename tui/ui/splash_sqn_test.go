//go:build !notui

package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/client"
	"github.com/AutumnsGrove/nova/tui/layout"
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

// splashModel builds a truecolor model whose banner can animate.
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
	want, _, ok := splash.Render(m.layout.Banner.W, m.layout.Banner.H, m.st, m.profile, m.version, 2*time.Second)
	if !ok {
		t.Fatal("reference render not ok")
	}
	if got := m.bannerView(); got != want {
		t.Fatalf("bannerView does not match Render at 2s")
	}
}

func TestUI_SQN35_KeyKeepsTheBannerAnimating(t *testing.T) {
	m, clk := splashModel(t, 120, 60)
	m.scheduleTick()
	update(m, runeKey('a'))
	if !m.showSplash() {
		t.Fatal("a key dismissed the banner")
	}
	if !m.splashAnimated() {
		t.Fatal("the banner stopped animating after a key")
	}
	// The pending tick still fires and arms its successor.
	update(m, tickMsg{})
	if len(clk.durs) != 2 {
		t.Fatalf("ticks = %v, want the original and a follow-up", clk.durs)
	}
}

func TestUI_SQN36_EventKeepsTheBannerAnimating(t *testing.T) {
	m, _ := splashModel(t, 120, 60)
	m.scheduleTick()
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
	if !m.showSplash() {
		t.Fatal("an event dismissed the banner")
	}
	if !m.splashAnimated() {
		t.Fatal("the banner stopped animating after an event")
	}
}

func TestUI_SQN37_NoTickWhenOffOrDone(t *testing.T) {
	m, _ := splashModel(t, 120, 60)
	m.display.Splash = false
	m.recompute()
	m.ticking = false
	if m.scheduleTick() != nil {
		t.Fatal("tick scheduled with the splash off")
	}

	done, _ := splashModel(t, 120, 60)
	done.splashAt = splash.Sequence
	done.recompute()
	done.ticking = false
	if done.scheduleTick() != nil {
		t.Fatal("tick scheduled after the sequence collapsed the banner")
	}

	plain, _, _ := newModel(t)
	plain.ticking = false
	if plain.scheduleTick() != nil {
		t.Fatal("tick scheduled for a plain tier")
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

	m.splashAt = splash.Sequence
	m.recompute()
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
	// A narrow screen no longer fits the Lockup banner, so the tier degrades.
	update(m, tea.WindowSizeMsg{Width: 30, Height: 60})
	if m.splashAt != 3*time.Second {
		t.Fatalf("resize changed splashAt to %v", m.splashAt)
	}
	_, tier, ok := splash.Render(m.layout.Banner.W, m.layout.Banner.H, m.st, m.profile, m.version, m.splashAt)
	if !ok || tier != splash.Plain {
		t.Fatalf("tier after resize = %v ok=%v, want Plain", tier, ok)
	}
}

// SQN53: past Sequence the animation freezes on the final lockup frame, which
// stays at the top; no further tick is armed.
func TestUI_SQN53_HoldsFinalFrame(t *testing.T) {
	m, clk := splashModel(t, 120, 60)
	m.scheduleTick()
	clk.now = clk.now.Add(splash.Sequence + time.Second)
	update(m, tickMsg{})
	if m.splashAt != splash.Sequence {
		t.Fatalf("splashAt = %v, want it clamped to Sequence", m.splashAt)
	}
	if m.layout.Banner.H != splash.LockupHeight {
		t.Fatalf("banner region = %d, want the held lockup %d", m.layout.Banner.H, splash.LockupHeight)
	}
	if m.layout.Header.H != 1 {
		t.Fatal("the header is not shown below the banner")
	}
	clk.durs = nil
	if m.scheduleTick() != nil {
		t.Fatal("a tick was armed after the hold")
	}
}

// SQN58: typing does not dismiss the banner, but sending the first prompt does.
func TestUI_SQN58_FirstPromptDismissesTheBanner(t *testing.T) {
	m, _ := splashModel(t, 120, 60)
	update(m, runeKey('x'))
	if m.layout.Banner.H == 0 {
		t.Fatal("typing dismissed the banner")
	}
	m.setEditorText("hello")
	update(m, enterKey)
	if m.layout.Banner.H != 0 {
		t.Fatalf("banner region = %d, want 0 after the first prompt", m.layout.Banner.H)
	}
	if m.layout.Header.H != 1 {
		t.Fatal("the header did not return after the first prompt")
	}
}

// SQN54: the banner is the top region; the header, transcript, and editor sit
// below it and the editor keeps what the user typed.
func TestUI_SQN54_BannerIsATopRegion(t *testing.T) {
	m, _ := splashModel(t, 120, 60)
	m.setEditorText("hello")
	if m.layout.Banner.H != layout.MaxBannerLines {
		t.Fatalf("banner height = %d, want %d", m.layout.Banner.H, layout.MaxBannerLines)
	}
	if m.layout.Banner.Y != 0 || m.layout.Header.Y != m.layout.Banner.H {
		t.Fatalf("banner is not the top region: %+v", m.layout)
	}
	if m.layout.Main.H < 3 || m.layout.Editor.H < 1 {
		t.Fatalf("the banner squeezed the screen: %+v", m.layout)
	}
	if m.editor.Value() != "hello" {
		t.Fatalf("editor value = %q, want hello", m.editor.Value())
	}
	// A click in the transcript reaches the list and leaves the editor alone.
	update(m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 1, Y: m.layout.Main.Y})
	if m.editor.Value() != "hello" {
		t.Fatalf("a transcript click changed the editor: %q", m.editor.Value())
	}
}

// SQN56: the banner plays a tall hero, steps down to the lockup, then
// collapses to the header.
func TestUI_SQN56_HeroThenLockupThenHeader(t *testing.T) {
	m, clk := splashModel(t, 120, 60)
	hero := m.layout.Banner
	if hero.H != layout.MaxBannerLines {
		t.Fatalf("hero banner height = %d, want the Full tier %d", hero.H, layout.MaxBannerLines)
	}
	if _, tier, ok := splash.Render(hero.W, hero.H, m.st, m.profile, m.version, m.splashAt); !ok || tier != splash.Full {
		t.Fatalf("hero tier = %v ok=%v, want Full", tier, ok)
	}

	// At half the sequence the banner steps down to the one-line lockup.
	m.scheduleTick()
	clk.now = clk.now.Add(splashHeroUntil)
	update(m, tickMsg{})
	if m.layout.Banner.H != splash.LockupHeight {
		t.Fatalf("settled banner height = %d, want %d", m.layout.Banner.H, splash.LockupHeight)
	}

	// At Sequence the animation stops on the held lockup frame.
	clk.now = clk.now.Add(splash.Sequence)
	update(m, tickMsg{})
	if m.layout.Banner.H != splash.LockupHeight {
		t.Fatalf("banner height after Sequence = %d, want the held lockup %d", m.layout.Banner.H, splash.LockupHeight)
	}
	if m.layout.Header.H != 1 {
		t.Fatalf("header height after Sequence = %d, want 1", m.layout.Header.H)
	}
}
