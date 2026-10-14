//go:build !notui

package ui

import (
	"context"
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

func TestUI_SGN11_PickerShowsReady(t *testing.T) {
	store := &fakeStore{ready: true, key: "stored"}
	fakeRegister(t, "fake-ready-c22", []providerauth.Method{providerauth.APIKey}, store, nil)
	m, _, _ := newModel(t)
	m.setEditorText("/login")
	drive(m, update(m, enterKey))
	if v := m.render(); !strings.Contains(v, "ready") || !strings.Contains(v, "store") {
		t.Fatalf("ready state missing:\n%s", v)
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
	execAll(batch)
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
