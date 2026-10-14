//go:build !notui

package ui

import (
	"context"
	"encoding/json"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/proto"
)

// fakeStore is an in-memory credential store for the UI tests.
type fakeStore struct {
	mu      sync.Mutex
	key     string
	method  providerauth.Method
	saves   int
	deletes int
	ready   bool
}

func (s *fakeStore) save(string, providerauth.Credential) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	s.ready = true
	s.key = "stored-" + string(rune('0'+s.saves))
	return "fake-auth.json", nil
}

func (s *fakeStore) load(string) (providerauth.Credential, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return providerauth.Credential{}, false, nil
	}
	return providerauth.Credential{Method: s.method, Key: s.key}, true, nil
}

func (s *fakeStore) delete(string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	s.ready = false
	s.key = ""
	return nil
}

func (s *fakeStore) snapshot() (key string, saves, deletes int, ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.key, s.saves, s.deletes, s.ready
}

// fakeSession is a started interactive sign-in the test completes.
type fakeSession struct {
	prompt, url, code string
	ch                chan providerauth.Credential
	mu                sync.Mutex
	submitted         []string
	closed            bool
}

func newFakeSession() *fakeSession {
	return &fakeSession{prompt: "Sign in with Fake", url: "https://fake.example/auth", ch: make(chan providerauth.Credential, 1)}
}

func (s *fakeSession) Prompt() string { return s.prompt }
func (s *fakeSession) URL() string    { return s.url }
func (s *fakeSession) Code() string   { return s.code }

func (s *fakeSession) Submit(input string) error {
	s.mu.Lock()
	s.submitted = append(s.submitted, input)
	s.mu.Unlock()
	s.ch <- providerauth.Credential{Method: providerauth.OAuth, Key: "oauth-key"}
	return nil
}

func (s *fakeSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *fakeSession) Wait(ctx context.Context) (providerauth.Credential, error) {
	select {
	case <-ctx.Done():
		return providerauth.Credential{}, ctx.Err()
	case c := <-s.ch:
		return c, nil
	}
}

func (s *fakeSession) complete(c providerauth.Credential) { s.ch <- c }

func (s *fakeSession) wasClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *fakeSession) lastSubmit() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.submitted) == 0 {
		return ""
	}
	return s.submitted[len(s.submitted)-1]
}

// fakeRegister registers a provider with the given methods and store.
func fakeRegister(t *testing.T, id string, methods []providerauth.Method, store *fakeStore, begin map[providerauth.Method]providerauth.BeginFunc) {
	t.Helper()
	providerauth.Register(providerauth.Provider{
		ID: id, Name: "Fake " + id, Methods: methods,
		Begin: begin, Save: store.save, Load: store.load, Delete: store.delete,
	})
}

// drive runs one command and feeds the single message it returns, returning
// the command Update produced in response.
func drive(m *Model, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	msg := do(cmd)
	if msg == nil {
		return nil
	}
	return update(m, msg)
}

// typeKeys feeds a string to the focused dialog one rune at a time.
func typeKeys(m *Model, s string) {
	for _, r := range s {
		update(m, runeKey(r))
	}
}

func TestUI_SGN10_LoginOpensPicker(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("/login")
	drive(m, update(m, enterKey))
	if m.overlay.Len() != 1 {
		t.Fatalf("overlay depth = %d, want 1", m.overlay.Len())
	}
	if v := m.render(); !strings.Contains(v, "Select provider to configure:") {
		t.Fatalf("provider picker missing:\n%s", v)
	}
}

// providersResponse is a canned list_providers answer.
func providersResponse(rows ...string) proto.Response {
	return proto.Response{Command: proto.CmdListProviders, OK: true, Data: json.RawMessage("[" + strings.Join(rows, ",") + "]")}
}

func TestUI_SGN11_PickerShowsReady(t *testing.T) {
	m, f, _ := newModel(t)
	f.responses[proto.CmdListProviders] = providersResponse(
		`{"id":"fake-ready-c22","name":"Fake Ready","methods":["api_key"],"ready":true,"source":"store"}`)
	m.setEditorText("/login")
	drive(m, update(m, enterKey))
	if v := m.render(); !strings.Contains(v, "Fake Ready") || !strings.Contains(v, "ready") || !strings.Contains(v, "store") {
		t.Fatalf("ready state missing:\n%s", v)
	}
}

func TestUI_SGN45_PickerAsksTheServer(t *testing.T) {
	m, f, _ := newModel(t)
	m.setEditorText("/login")
	drive(m, update(m, enterKey))
	if len(f.sends) != 1 || f.sends[0].Type != proto.CmdListProviders {
		t.Fatalf("sends = %+v, want one list_providers", f.sends)
	}
}

func TestUI_SGN46_PickerShowsAConfigKeyAsReady(t *testing.T) {
	m, f, _ := newModel(t)
	// The server resolves the loaded config; the TUI never sees it, so a
	// hand-edited key must reach the picker through the response alone.
	f.responses[proto.CmdListProviders] = providersResponse(
		`{"id":"opencode-go","name":"OpenCode Go","methods":["device","api_key"],"ready":true,"source":"config"}`)
	m.setEditorText("/login")
	drive(m, update(m, enterKey))
	// The exact note, not loose words: the picker title itself contains "config".
	if v := m.render(); !strings.Contains(v, "device, API key · ready (config)") {
		t.Fatalf("config-key readiness missing:\n%s", v)
	}
}

func TestUI_SGN12_MethodPicker(t *testing.T) {
	fakeRegister(t, "fake-two-c22", []providerauth.Method{providerauth.OAuth, providerauth.APIKey}, &fakeStore{}, nil)
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-two-c22")
	drive(m, update(m, enterKey))
	if v := m.render(); !strings.Contains(v, "Select authentication method for Fake fake-two-c22:") {
		t.Fatalf("method picker missing:\n%s", v)
	}
}

func TestUI_SGN13_DeviceMethod(t *testing.T) {
	fakeRegister(t, "fake-device-c22", []providerauth.Method{providerauth.Device, providerauth.APIKey}, &fakeStore{}, nil)
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-device-c22")
	drive(m, update(m, enterKey))
	if v := m.render(); !strings.Contains(v, "device") {
		t.Fatalf("device method missing:\n%s", v)
	}
}

func TestUI_SGN14_KeyMethodDirect(t *testing.T) {
	fakeRegister(t, "fake-key-c22", []providerauth.Method{providerauth.OAuth, providerauth.APIKey}, &fakeStore{}, nil)
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-key-c22 api_key")
	drive(m, update(m, enterKey))
	if v := m.render(); !strings.Contains(v, "Paste your Fake fake-key-c22 key") {
		t.Fatalf("secret input missing:\n%s", v)
	}
}

func TestUI_SGN15_UnknownProvider(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("/login nope")
	update(m, enterKey)
	if !strings.Contains(m.notice, "unknown provider") || m.overlay.Len() != 0 {
		t.Fatalf("notice = %q overlay = %d", m.notice, m.overlay.Len())
	}
}

func TestUI_SGN16_EscClosesPicker(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("/login")
	drive(m, update(m, enterKey))
	update(m, escKey)
	if m.overlay.Len() != 0 {
		t.Fatal("picker did not close on esc")
	}
}

func TestUI_SGN17_LogoutPicker(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("/logout")
	drive(m, update(m, enterKey))
	if v := m.render(); !strings.Contains(v, "Select provider to logout:") {
		t.Fatalf("logout picker missing:\n%s", v)
	}
}

func TestUI_SGN19_KeySaves(t *testing.T) {
	store := &fakeStore{}
	fakeRegister(t, "fake-save-c22", []providerauth.Method{providerauth.APIKey}, store, nil)
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-save-c22 api_key")
	drive(m, update(m, enterKey))
	typeKeys(m, "sk-test")
	drive(m, update(m, enterKey))
	if key, saves, _, _ := store.snapshot(); saves != 1 || key == "" {
		t.Fatalf("store = key %q saves %d", key, saves)
	}
}

func TestUI_SGN20_EscCancelsKey(t *testing.T) {
	store := &fakeStore{}
	fakeRegister(t, "fake-cancel-c22", []providerauth.Method{providerauth.APIKey}, store, nil)
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-cancel-c22 api_key")
	drive(m, update(m, enterKey))
	typeKeys(m, "sk-test")
	update(m, escKey)
	if _, saves, _, _ := store.snapshot(); saves != 0 {
		t.Fatal("esc still saved the key")
	}
}

func TestUI_SGN22_NoticeNamesPathNotKey(t *testing.T) {
	store := &fakeStore{}
	fakeRegister(t, "fake-notice-c22", []providerauth.Method{providerauth.APIKey}, store, nil)
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-notice-c22 api_key")
	drive(m, update(m, enterKey))
	typeKeys(m, "sk-secret")
	drive(m, update(m, enterKey))
	if !strings.Contains(m.notice, "fake-auth.json") {
		t.Fatalf("notice missing the path: %q", m.notice)
	}
	if strings.Contains(m.notice, "sk-secret") {
		t.Fatalf("notice leaked the key: %q", m.notice)
	}
}

// beginOAuth starts a fake OAuth flow and returns the wait command the model
// produced, with the flow dialog still open.
func beginOAuth(t *testing.T, id string, session *fakeSession) (*Model, *fakeStore, tea.Cmd) {
	t.Helper()
	store := &fakeStore{}
	fakeRegister(t, id, []providerauth.Method{providerauth.OAuth, providerauth.APIKey}, store, map[providerauth.Method]providerauth.BeginFunc{
		providerauth.OAuth: func(context.Context, providerauth.Config) (providerauth.Session, error) { return session, nil },
	})
	m, _, _ := newModel(t)
	m.setEditorText("/login " + id + " oauth")
	wait := drive(m, update(m, enterKey))
	return m, store, wait
}

func TestUI_SGN23_OAuthDialogShowsURL(t *testing.T) {
	m, _, _ := beginOAuth(t, "fake-oauth-c22", newFakeSession())
	if v := m.render(); !strings.Contains(v, "https://fake.example/auth") {
		t.Fatalf("flow dialog missing the URL:\n%s", v)
	}
}

func TestUI_SGN25_PastedRedirectCompletes(t *testing.T) {
	m, store, wait := beginOAuth(t, "fake-paste-c22", newFakeSession())
	typeKeys(m, "https://127.0.0.1/callback?code=x&state=y")
	drive(m, update(m, enterKey))
	drive(m, drive(m, wait))
	if _, saves, _, _ := store.snapshot(); saves != 1 {
		t.Fatalf("credential not saved: saves=%d", saves)
	}
}

func TestUI_SGN29_DevicePollCompletes(t *testing.T) {
	session := &fakeSession{prompt: "Sign in with Device", url: "https://device.example", code: "CODE-1", ch: make(chan providerauth.Credential, 1)}
	store := &fakeStore{}
	fakeRegister(t, "fake-device-poll-c22", []providerauth.Method{providerauth.Device, providerauth.APIKey}, store, map[providerauth.Method]providerauth.BeginFunc{
		providerauth.Device: func(context.Context, providerauth.Config) (providerauth.Session, error) { return session, nil },
	})
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-device-poll-c22 device")
	wait := drive(m, update(m, enterKey))
	if v := m.render(); !strings.Contains(v, "https://device.example") || !strings.Contains(v, "CODE-1") {
		t.Fatalf("device dialog missing the URL or code:\n%s", v)
	}
	session.complete(providerauth.Credential{Method: providerauth.Device, Key: "device-key"})
	drive(m, drive(m, wait))
	if _, saves, _, _ := store.snapshot(); saves != 1 {
		t.Fatalf("device poll did not save: saves=%d", saves)
	}
}

// pump runs a command and feeds every message it yields back into the model,
// following the commands those updates return, so a test can drive a chain of
// request, response, and follow-up request to its end.
func pump(m *Model, cmd tea.Cmd) {
	for depth := 0; cmd != nil && depth < 20; depth++ {
		msg := do(cmd)
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				pump(m, c)
			}
			return
		}
		if msg == nil {
			return
		}
		cmd = update(m, msg)
	}
}

// sentTypes lists the command types the fake client has received.
func sentTypes(f *fakeClient) []string {
	var out []string
	for _, c := range f.sends {
		out = append(out, c.Type)
	}
	return out
}

// loginWithKey signs in to a fake API-key provider through the dialog and
// returns the command the save produced.
func loginWithKey(t *testing.T, m *Model, id string) tea.Cmd {
	t.Helper()
	m.setEditorText("/login " + id + " api_key")
	drive(m, update(m, enterKey))
	typeKeys(m, "sk-test")
	return drive(m, update(m, enterKey))
}

func registerKeyProvider(t *testing.T, id string) {
	t.Helper()
	store := &fakeStore{}
	providerauth.Register(providerauth.Provider{
		ID: id, Name: "Fake Model", Methods: []providerauth.Method{providerauth.APIKey},
		DefaultModel: "fake/model",
		Save:         store.save, Load: store.load, Delete: store.delete,
	})
}

// modelsResponse is a canned list_models answer with one model.
func modelsResponse() proto.Response {
	return proto.Response{Command: proto.CmdListModels, OK: true,
		Data: json.RawMessage(`[{"id":"fake/model","name":"Fake Model","provider":"fake"}]`)}
}

func TestUI_SGN34_DefaultModelOpensPicker(t *testing.T) {
	// The server cannot say which provider serves the current model, so the
	// safe choice is to open the picker.
	registerKeyProvider(t, "fake-model-c22")
	m, f, _ := newModel(t)
	f.responses[proto.CmdListModels] = modelsResponse()
	pump(m, loginWithKey(t, m, "fake-model-c22"))
	if v := m.render(); !strings.Contains(v, "Fake Model") {
		t.Fatalf("model picker did not open; sent %v:\n%s", sentTypes(f), v)
	}
}

func TestUI_SGN49_NoPickerWhenTheCurrentModelIsReady(t *testing.T) {
	registerKeyProvider(t, "fake-keep-c22")
	m, f, _ := newModel(t)
	f.responses[proto.CmdListProviders] = providersResponse(
		`{"id":"openrouter","name":"OpenRouter","methods":["api_key"],"ready":true,"source":"env","current":true}`,
		`{"id":"fake-keep-c22","name":"Fake Model","methods":["api_key"],"ready":true,"source":"store"}`)
	pump(m, loginWithKey(t, m, "fake-keep-c22"))
	for _, typ := range sentTypes(f) {
		if typ == proto.CmdListModels {
			t.Fatalf("the model picker was requested although the current model works; sent %v", sentTypes(f))
		}
	}
	if m.overlay.Len() != 0 {
		t.Fatal("a dialog opened after a login that needed no model choice")
	}
	if !strings.Contains(m.notice, "/model") {
		t.Fatalf("notice = %q, want a hint to use /model", m.notice)
	}
}

func TestUI_SGN50_PickerWhenTheCurrentModelIsNotReady(t *testing.T) {
	registerKeyProvider(t, "fake-need-c22")
	m, f, _ := newModel(t)
	f.responses[proto.CmdListModels] = modelsResponse()
	f.responses[proto.CmdListProviders] = providersResponse(
		`{"id":"openrouter","name":"OpenRouter","methods":["api_key"],"ready":false,"current":true}`,
		`{"id":"fake-need-c22","name":"Fake Model","methods":["api_key"],"ready":true,"source":"store"}`)
	pump(m, loginWithKey(t, m, "fake-need-c22"))
	if v := m.render(); !strings.Contains(v, "Fake Model") {
		t.Fatalf("model picker did not open; sent %v:\n%s", sentTypes(f), v)
	}
}

func TestUI_SGN30_EscClosesFlow(t *testing.T) {
	session := newFakeSession()
	m, store, _ := beginOAuth(t, "fake-esc-c22", session)
	update(m, escKey)
	if !session.wasClosed() {
		t.Fatal("esc did not close the session")
	}
	if _, saves, _, _ := store.snapshot(); saves != 0 {
		t.Fatal("esc saved a credential")
	}
}

func TestUI_SGN31_ClosedFlowCannotSaveLater(t *testing.T) {
	session := newFakeSession()
	m, store, _ := beginOAuth(t, "fake-late-c22", session)
	gen := m.loginGen
	update(m, escKey)
	update(m, loginDoneMsg{gen: gen, cred: providerauth.Credential{Method: providerauth.OAuth, Key: "late"}})
	if _, saves, _, _ := store.snapshot(); saves != 0 {
		t.Fatal("a closed flow still saved a credential")
	}
}

func TestUI_SGN35_LogoutDeletes(t *testing.T) {
	store := &fakeStore{ready: true, key: "stored"}
	fakeRegister(t, "fake-logout-c22", []providerauth.Method{providerauth.APIKey}, store, nil)
	m, _, _ := newModel(t)
	m.setEditorText("/logout fake-logout-c22")
	drive(m, update(m, enterKey))
	if _, _, deletes, ready := store.snapshot(); deletes != 1 || ready {
		t.Fatalf("delete = %d, ready = %v", deletes, ready)
	}
	if !strings.Contains(m.notice, "logged out") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestUI_SGN36_LogoutKeepsEnv(t *testing.T) {
	store := &fakeStore{ready: true, key: "stored"}
	t.Setenv("FAKE_ENV_KEY_C22", "env-key")
	providerauth.Register(providerauth.Provider{
		ID: "fake-env-c22", Name: "Fake Env", Methods: []providerauth.Method{providerauth.APIKey},
		EnvVars: []string{"FAKE_ENV_KEY_C22"},
		Save:    store.save, Load: store.load, Delete: store.delete,
	})
	m, _, _ := newModel(t)
	m.setEditorText("/logout fake-env-c22")
	drive(m, update(m, enterKey))
	if !strings.Contains(m.notice, "still configured from env") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestUI_SGN33_LoginRefreshesCatalog(t *testing.T) {
	store := &fakeStore{}
	fakeRegister(t, "fake-refresh-c22", []providerauth.Method{providerauth.APIKey}, store, nil)
	m, f, _ := newModel(t)
	m.setEditorText("/login fake-refresh-c22 api_key")
	drive(m, update(m, enterKey))
	typeKeys(m, "sk-test")
	batch := drive(m, update(m, enterKey))
	// list_models now follows the list_providers answer, so drive the chain.
	pump(m, batch)
	foundCommands, foundModels := false, false
	for _, c := range f.sends {
		if c.Type == proto.CmdGetCommands {
			foundCommands = true
		}
		if c.Type == proto.CmdListModels {
			foundModels = true
		}
	}
	if !foundCommands || !foundModels {
		t.Fatalf("catalog refresh sends = %+v", f.sends)
	}
}

func TestUI_SGN38_NoSecretInRenderedLines(t *testing.T) {
	store := &fakeStore{}
	fakeRegister(t, "fake-secret-c22", []providerauth.Method{providerauth.APIKey}, store, nil)
	m, _, _ := newModel(t)
	m.setEditorText("/login fake-secret-c22 api_key")
	drive(m, update(m, enterKey))
	typeKeys(m, "sk-super-secret")
	if v := m.render(); strings.Contains(v, "sk-super-secret") {
		t.Fatalf("secret appeared in the view:\n%s", v)
	}
}

func TestProperty_SGN_Secrets(t *testing.T) {
	r := rand.New(rand.NewSource(22))
	store := &fakeStore{}
	fakeRegister(t, "fake-prop-c22", []providerauth.Method{providerauth.APIKey}, store, nil)
	for i := 0; i < 50; i++ {
		key := "sk-" + strconv.Itoa(r.Int())
		m, _, _ := newModel(t)
		m.setEditorText("/login fake-prop-c22 api_key")
		drive(m, update(m, enterKey))
		typeKeys(m, key)
		if v := m.render(); strings.Contains(v, key) {
			t.Fatalf("secret %q appeared in the view", key)
		}
	}
}

func TestUI_SGN40_LoginDuringRun(t *testing.T) {
	store := &fakeStore{}
	fakeRegister(t, "fake-run-c22", []providerauth.Method{providerauth.APIKey}, store, nil)
	m, _, _ := newModel(t)
	m.runActive = true
	m.setEditorText("/login fake-run-c22")
	drive(m, update(m, enterKey))
	if m.overlay.Len() == 0 {
		t.Fatal("login dialog did not open during a run")
	}
	if !m.runActive {
		t.Fatal("the run state was disturbed")
	}
}

// logoutOf signs a fake provider out through /logout and drives the chain.
func logoutOf(t *testing.T, m *Model, id string) {
	t.Helper()
	m.setEditorText("/logout " + id)
	pump(m, update(m, enterKey))
}

func setModelSends(f *fakeClient) []string {
	var out []string
	for _, c := range f.sends {
		if c.Type == proto.CmdSetModel {
			out = append(out, c.Model)
		}
	}
	return out
}

func TestUI_SGN51_LogoutSwitchesToAnotherReadyProvider(t *testing.T) {
	fakeRegister(t, "fake-out-a-c22", []providerauth.Method{providerauth.APIKey}, &fakeStore{ready: true, key: "k"}, nil)
	m, f, _ := newModel(t)
	f.responses[proto.CmdListProviders] = providersResponse(
		`{"id":"fake-out-a-c22","name":"Fake A","methods":["api_key"],"ready":false,"current":true,"default_model":"a/model"}`,
		`{"id":"fake-out-b-c22","name":"Fake B","methods":["api_key"],"ready":true,"source":"env","default_model":"b/model"}`)
	logoutOf(t, m, "fake-out-a-c22")
	if got := setModelSends(f); len(got) != 1 || got[0] != "b/model" {
		t.Fatalf("set_model sends = %v, want [b/model]", got)
	}
	if !strings.Contains(m.notice, "switched to b/model") || !strings.Contains(m.notice, "Fake B") {
		t.Fatalf("notice = %q, want it to say what the model became", m.notice)
	}
}

func TestUI_SGN52_LogoutWithNothingToSwitchTo(t *testing.T) {
	fakeRegister(t, "fake-out-c-c22", []providerauth.Method{providerauth.APIKey}, &fakeStore{ready: true, key: "k"}, nil)
	m, f, _ := newModel(t)
	f.responses[proto.CmdListProviders] = providersResponse(
		`{"id":"fake-out-c-c22","name":"Fake C","methods":["api_key"],"ready":false,"current":true,"default_model":"c/model"}`)
	logoutOf(t, m, "fake-out-c-c22")
	if got := setModelSends(f); len(got) != 0 {
		t.Fatalf("set_model sent with no ready provider: %v", got)
	}
	if !strings.Contains(m.notice, "logged out of Fake fake-out-c-c22") || !strings.Contains(m.notice, "/login") {
		t.Fatalf("notice = %q, want a hint to sign in again", m.notice)
	}
}

func TestUI_SGN53_LogoutOfAnotherProviderKeepsTheModel(t *testing.T) {
	fakeRegister(t, "fake-out-d-c22", []providerauth.Method{providerauth.APIKey}, &fakeStore{ready: true, key: "k"}, nil)
	m, f, _ := newModel(t)
	// The provider logged out is not the one serving the current model.
	f.responses[proto.CmdListProviders] = providersResponse(
		`{"id":"fake-out-d-c22","name":"Fake D","methods":["api_key"],"ready":false,"default_model":"d/model"}`,
		`{"id":"openrouter","name":"OpenRouter","methods":["api_key"],"ready":true,"source":"env","current":true,"default_model":"or/model"}`)
	logoutOf(t, m, "fake-out-d-c22")
	if got := setModelSends(f); len(got) != 0 {
		t.Fatalf("the model was changed although the current provider still works: %v", got)
	}
}

func TestUI_SGN54_LogoutWhileStillConfiguredKeepsTheModel(t *testing.T) {
	t.Setenv("FAKE_OUT_E_KEY", "from-env")
	providerauth.Register(providerauth.Provider{
		ID: "fake-out-e-c22", Name: "Fake E", Methods: []providerauth.Method{providerauth.APIKey},
		EnvVars: []string{"FAKE_OUT_E_KEY"},
		Save:    (&fakeStore{}).save, Load: (&fakeStore{}).load, Delete: (&fakeStore{}).delete,
	})
	m, f, _ := newModel(t)
	logoutOf(t, m, "fake-out-e-c22")
	if got := setModelSends(f); len(got) != 0 {
		t.Fatalf("set_model sent although the env key still works: %v", got)
	}
	if !strings.Contains(m.notice, "still configured from env") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestUI_SGN55_LogoutSaysWhenAConfigKeyRemains(t *testing.T) {
	fakeRegister(t, "fake-out-f-c22", []providerauth.Method{providerauth.APIKey}, &fakeStore{ready: true, key: "k"}, nil)
	m, f, _ := newModel(t)
	// Locally nothing remains, but the server still resolves a key from its
	// config, so the provider keeps working and the notice must say so.
	f.responses[proto.CmdListProviders] = providersResponse(
		`{"id":"fake-out-f-c22","name":"Fake F","methods":["api_key"],"ready":true,"source":"config","current":true,"default_model":"f/model"}`)
	logoutOf(t, m, "fake-out-f-c22")
	if !strings.Contains(m.notice, "still configured from config") {
		t.Fatalf("notice = %q, want it to name the remaining config key", m.notice)
	}
	if got := setModelSends(f); len(got) != 0 {
		t.Fatalf("model changed although the provider still works: %v", got)
	}
}
