package firstrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider"
)

// catalog is a small fixed catalog for the tests.
func catalog() []provider.Model {
	return []provider.Model{{ID: "a/model", Name: "A"}, {ID: "b/model", Name: "B"}}
}

// harness records what the flow saves.
type harness struct {
	savedKey   string
	savedModel string
	loginCalls int
	loginErr   error
}

func newHarness() *harness { return &harness{} }

func (h *harness) options() Options {
	return Options{
		Catalog:      catalog(),
		DefaultModel: "b/model",
		Login: func(context.Context) (string, error) {
			h.loginCalls++
			return "sk-login", h.loginErr
		},
		SaveKey:   func(key string) error { h.savedKey = key; return nil },
		SaveModel: func(id string) error { h.savedModel = id; return nil },
	}
}

// keyMsg turns a name or a single rune into a key press.
func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
	}
}

// key sends one key press and returns the model.
func key(t *testing.T, m Flow, k string) Flow {
	t.Helper()
	next, _ := m.Update(keyMsg(k))
	out, ok := next.(Flow)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return out
}

// view renders the model's current screen as text.
func view(m Flow) string { return m.View().Content }

// startLogin presses enter on the login choice and delivers the login result.
func startLogin(t *testing.T, m Flow) (Flow, loginDoneMsg) {
	t.Helper()
	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Flow)
	if cmd == nil {
		t.Fatal("enter did not start the login command")
	}
	return m, cmd().(loginDoneMsg)
}

func TestFirstRun_FRS01_StartsAtLogin(t *testing.T) {
	h := newHarness()
	m := New(h.options())
	if m.Step() != Login {
		t.Fatalf("step = %v, want Login", m.Step())
	}
	text := view(m)
	for _, want := range []string{"Welcome", "Log in with OpenRouter", "Paste a key"} {
		if !strings.Contains(text, want) {
			t.Fatalf("welcome/login view missing %q:\n%s", want, text)
		}
	}
}

func TestFirstRun_FRS02_LoginSuccess(t *testing.T) {
	h := newHarness()
	m, msg := startLogin(t, New(h.options()))
	if msg.err != nil {
		t.Fatalf("login command: %v", msg.err)
	}
	next, _ := m.Update(msg)
	m = next.(Flow)
	if h.savedKey != "sk-login" || !m.Result().KeySaved {
		t.Fatalf("key not saved: harness=%q result=%+v", h.savedKey, m.Result())
	}
	if m.Step() != Model {
		t.Fatalf("step = %v, want Model", m.Step())
	}
}

func TestFirstRun_FRS03_PasteKeyMasked(t *testing.T) {
	h := newHarness()
	m := New(h.options())
	m = key(t, m, "down") // choose Paste a key
	m = key(t, m, "enter")
	for _, r := range "sk-secret" {
		m = key(t, m, string(r))
	}
	text := view(m)
	if strings.Contains(text, "sk-secret") {
		t.Fatalf("the key appears on screen:\n%s", text)
	}
	if !strings.Contains(text, "••") {
		t.Fatalf("no masked input shown:\n%s", text)
	}
	m = key(t, m, "enter")
	if h.savedKey != "sk-secret" {
		t.Fatalf("saved key = %q", h.savedKey)
	}
	if m.Step() != Model {
		t.Fatalf("step = %v, want Model", m.Step())
	}
}

func TestFirstRun_FRS04_EmptyKeyRejected(t *testing.T) {
	h := newHarness()
	m := New(h.options())
	m = key(t, m, "down")
	m = key(t, m, "enter")
	m = key(t, m, " ")
	m = key(t, m, "enter")
	if h.savedKey != "" || m.Result().KeySaved {
		t.Fatalf("an empty key was accepted: %q", h.savedKey)
	}
	if !strings.Contains(view(m), "empty") {
		t.Fatalf("no rejection message:\n%s", view(m))
	}
}

func TestFirstRun_FRS05_LoginFailure(t *testing.T) {
	h := newHarness()
	h.loginErr = errors.New("network down")
	m, msg := startLogin(t, New(h.options()))
	next, _ := m.Update(msg)
	m = next.(Flow)
	if !strings.Contains(view(m), "network down") {
		t.Fatalf("login error not shown:\n%s", view(m))
	}
	if m.Step() != Login || m.Result().KeySaved {
		t.Fatalf("state after failure: step=%v result=%+v", m.Step(), m.Result())
	}
	// enter retries, and the retry command runs.
	m, _ = startLogin(t, m)
	if h.loginCalls != 2 {
		t.Fatalf("login calls = %d, want 2 (a retry)", h.loginCalls)
	}
	// esc skips.
	m = key(t, m, "esc")
	if !m.Result().Skipped {
		t.Fatal("esc did not skip")
	}
}

func TestFirstRun_FRS06_EscSkips(t *testing.T) {
	h := newHarness()
	m := New(h.options())
	m = key(t, m, "esc")
	if !m.Result().Skipped || !m.done {
		t.Fatalf("esc did not skip: %+v done=%v", m.Result(), m.done)
	}
	if m.Step() == Done {
		t.Fatal("skip should end the flow, not advance to Done")
	}
}

// toModelStep logs in successfully and returns the model at the picker.
func toModelStep(t *testing.T, h *harness) Flow {
	t.Helper()
	m, msg := startLogin(t, New(h.options()))
	next, _ := m.Update(msg)
	return next.(Flow)
}

func TestFirstRun_FRS07_ModelDefaultAndSave(t *testing.T) {
	h := newHarness()
	m := toModelStep(t, h)
	if m.cursor != 1 {
		t.Fatalf("default cursor = %d, want 1 (b/model)", m.cursor)
	}
	if !strings.Contains(view(m), "> b/model") {
		t.Fatalf("default model is not highlighted:\n%s", view(m))
	}
	m = key(t, m, "enter")
	if h.savedModel != "b/model" || !m.Result().ModelSaved {
		t.Fatalf("model not saved: %q %+v", h.savedModel, m.Result())
	}
}

func TestFirstRun_FRS08_NotRunWithKey(t *testing.T) {
	if steps := Needed(true, &config.Config{}); len(steps) != 0 {
		t.Fatalf("Needed(true) = %v, want none", steps)
	}
}

func TestFirstRun_FRS11_KeyNeverOnScreen(t *testing.T) {
	h := newHarness()
	m := New(h.options())
	m = key(t, m, "down")
	m = key(t, m, "enter")
	for _, r := range "sk-super-secret" {
		m = key(t, m, string(r))
		if strings.Contains(view(m), "sk-super-secret") {
			t.Fatalf("key leaked while typing:\n%s", view(m))
		}
	}
}

func TestFirstRun_FRS12_NeededWhenMissing(t *testing.T) {
	steps := Needed(false, &config.Config{})
	if len(steps) != 2 || steps[0] != Login || steps[1] != Model {
		t.Fatalf("Needed(false) = %v, want [Login Model]", steps)
	}
}
