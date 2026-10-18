package firstrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider"
)

// catalog is a small fixed catalog for the tests.
func catalog() []provider.Model {
	return []provider.Model{{ID: "a/model", Name: "A"}, {ID: "b/model", Name: "B"}}
}

// harness is a fake SignIn that records what the flow asked for and saved.
type harness struct {
	providers []providerauth.Provider
	session   providerauth.Session

	savedProvider string
	savedCred     providerauth.Credential
	savedModel    string

	begins    int
	gotMethod providerauth.Method
	beginErr  error
	saveErr   error
}

func newHarness() *harness {
	return &harness{providers: []providerauth.Provider{
		{ID: "one", Name: "One", Methods: []providerauth.Method{providerauth.OAuth, providerauth.APIKey}},
		{ID: "two", Name: "Two", Methods: []providerauth.Method{providerauth.Device, providerauth.APIKey}},
	}}
}

func (h *harness) Providers() []providerauth.Provider { return h.providers }

func (h *harness) Begin(_ context.Context, p providerauth.Provider, m providerauth.Method) (providerauth.Session, error) {
	h.begins++
	h.gotMethod = m
	if h.beginErr != nil {
		return nil, h.beginErr
	}
	if h.session != nil {
		return h.session, nil
	}
	return &fakeSession{cred: providerauth.Credential{Method: m, Key: "sk-flow"}}, nil
}

func (h *harness) Save(p providerauth.Provider, c providerauth.Credential) error {
	h.savedProvider = p.ID
	h.savedCred = c
	return h.saveErr
}

func (h *harness) options() Options {
	return Options{
		Catalog:      catalog(),
		DefaultModel: "b/model",
		SignIn:       h,
		SaveModel:    func(id string) error { h.savedModel = id; return nil },
	}
}

// fakeSession is one provider's interactive flow.
type fakeSession struct {
	prompt, url, code, status string
	cred                      providerauth.Credential
	err                       error
	closed                    bool
}

func (s *fakeSession) Prompt() string      { return s.prompt }
func (s *fakeSession) URL() string         { return s.url }
func (s *fakeSession) Code() string        { return s.code }
func (s *fakeSession) Status() string      { return s.status }
func (s *fakeSession) Submit(string) error { return nil }
func (s *fakeSession) Close() error        { s.closed = true; return nil }
func (s *fakeSession) Wait(context.Context) (providerauth.Credential, error) {
	return s.cred, s.err
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
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
	}
}

// keyCmd sends one key press and returns the model and its command.
func keyCmd(t *testing.T, m Flow, k string) (Flow, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(keyMsg(k))
	out, ok := next.(Flow)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return out, cmd
}

// deliver runs a command and feeds its message back to the flow.
func deliver(t *testing.T, m Flow, cmd tea.Cmd) (Flow, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return m, nil
	}
	next, nextCmd := m.Update(cmd())
	out, ok := next.(Flow)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return out, nextCmd
}

// act sends one key and drives every command it starts to completion, so a
// login runs Begin, Wait, and Save in one call.
func act(t *testing.T, m Flow, k string) Flow {
	t.Helper()
	m, cmd := keyCmd(t, m, k)
	for i := 0; cmd != nil; i++ {
		if i == 4 {
			t.Fatal("the command chain did not settle")
		}
		m, cmd = deliver(t, m, cmd)
	}
	return m
}

// view renders the model's current screen as text.
func view(m Flow) string { return m.View().Content }

// chooseTo moves the selection down row times and presses enter, returning the
// started command without running it.
func chooseTo(t *testing.T, m Flow, row int) (Flow, tea.Cmd) {
	t.Helper()
	for i := 0; i < row; i++ {
		m = act(t, m, "down")
	}
	return keyCmd(t, m, "enter")
}

func TestFirstRun_FRS01_StartsAtLogin(t *testing.T) {
	h := newHarness()
	m := New(h.options())
	if m.Step() != Login {
		t.Fatalf("step = %v, want Login", m.Step())
	}
	text := view(m)
	for _, want := range []string{"Welcome", "Sign in with One", "Paste a One key", "Sign in with Two", "Paste a Two key"} {
		if !strings.Contains(text, want) {
			t.Fatalf("welcome/login view missing %q:\n%s", want, text)
		}
	}
}

func TestFirstRun_FRS02_LoginSuccess(t *testing.T) {
	h := newHarness()
	m := act(t, New(h.options()), "enter")
	if h.savedCred.Key != "sk-flow" || h.savedProvider != "one" || !m.Result().KeySaved {
		t.Fatalf("credential not saved: provider=%q cred=%+v result=%+v", h.savedProvider, h.savedCred, m.Result())
	}
	if m.Step() != Model {
		t.Fatalf("step = %v, want Model", m.Step())
	}
}

func TestFirstRun_FRS03_PasteKeyMasked(t *testing.T) {
	h := newHarness()
	m, cmd := chooseTo(t, New(h.options()), 1) // Paste a One key
	if cmd != nil {
		t.Fatal("an API key choice started a flow")
	}
	for _, r := range "sk-secret" {
		m = act(t, m, string(r))
	}
	text := view(m)
	if strings.Contains(text, "sk-secret") {
		t.Fatalf("the key appears on screen:\n%s", text)
	}
	if !strings.Contains(text, "••") {
		t.Fatalf("no masked input shown:\n%s", text)
	}
	m = act(t, m, "enter")
	if h.savedCred.Key != "sk-secret" || h.savedProvider != "one" {
		t.Fatalf("saved = %q for %q", h.savedCred.Key, h.savedProvider)
	}
	if m.Step() != Model {
		t.Fatalf("step = %v, want Model", m.Step())
	}
}

func TestFirstRun_FRS04_EmptyKeyRejected(t *testing.T) {
	h := newHarness()
	m, _ := chooseTo(t, New(h.options()), 1)
	m = act(t, m, " ")
	m = act(t, m, "enter")
	if h.savedCred.Key != "" || m.Result().KeySaved {
		t.Fatalf("an empty key was accepted: %q", h.savedCred.Key)
	}
	if !strings.Contains(view(m), "empty") {
		t.Fatalf("no rejection message:\n%s", view(m))
	}
}

func TestFirstRun_FRS05_LoginFailure(t *testing.T) {
	h := newHarness()
	h.beginErr = errors.New("network down")
	m := act(t, New(h.options()), "enter")
	if !strings.Contains(view(m), "network down") {
		t.Fatalf("login error not shown:\n%s", view(m))
	}
	if m.Step() != Login || m.Result().KeySaved {
		t.Fatalf("state after failure: step=%v result=%+v", m.Step(), m.Result())
	}
	// enter retries, and the retry command runs.
	act(t, m, "enter")
	if h.begins != 2 {
		t.Fatalf("begins = %d, want 2 (a retry)", h.begins)
	}
	// esc skips.
	m = act(t, m, "esc")
	if !m.Result().Skipped {
		t.Fatal("esc did not skip")
	}
}

func TestFirstRun_FRS06_EscSkips(t *testing.T) {
	h := newHarness()
	m := act(t, New(h.options()), "esc")
	if !m.Result().Skipped || !m.done {
		t.Fatalf("esc did not skip: %+v done=%v", m.Result(), m.done)
	}
	if m.Step() == Done {
		t.Fatal("skip should end the flow, not advance to Done")
	}
}

// toModelStep logs in with a pasted key and returns the model at the picker.
func toModelStep(t *testing.T, h *harness) Flow {
	t.Helper()
	m, _ := chooseTo(t, New(h.options()), 1)
	for _, r := range "sk-key" {
		m = act(t, m, string(r))
	}
	return act(t, m, "enter")
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
	m = act(t, m, "enter")
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
	m, _ := chooseTo(t, New(h.options()), 1)
	for _, r := range "sk-super-secret" {
		m = act(t, m, string(r))
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

func TestFirstRun_FRS13_OffersEveryProvider(t *testing.T) {
	// No fake SignIn: the real registry must drive the list.
	m := New(Options{Catalog: catalog()})
	text := view(m)
	for _, want := range []string{"OpenRouter", "OpenCode Go"} {
		if !strings.Contains(text, want) {
			t.Fatalf("first-run does not offer %q:\n%s", want, text)
		}
	}
}

func TestFirstRun_FRS14_DeviceFlowShowsURLAndCode(t *testing.T) {
	h := newHarness()
	h.session = &fakeSession{url: "https://example.test/device", code: "ABCD-1234", status: "Waiting for the device"}
	m, cmd := chooseTo(t, New(h.options()), 2) // Sign in with Two (device)
	// Deliver only the started flow, so the wait view is rendered before Wait.
	m, _ = deliver(t, m, cmd)
	text := view(m)
	for _, want := range []string{"https://example.test/device", "ABCD-1234", "Waiting for the device"} {
		if !strings.Contains(text, want) {
			t.Fatalf("device flow view missing %q:\n%s", want, text)
		}
	}
}

func TestFirstRun_FRS15_ChosenMethodReachesBegin(t *testing.T) {
	h := newHarness()
	_, cmd := chooseTo(t, New(h.options()), 2) // device for Two
	if cmd == nil {
		t.Fatal("the device choice started no flow")
	}
	_ = cmd()
	if h.gotMethod != providerauth.Device || h.begins != 1 {
		t.Fatalf("Begin got method %q after %d calls, want device once", h.gotMethod, h.begins)
	}
}
