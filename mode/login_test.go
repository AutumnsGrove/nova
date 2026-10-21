package mode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider/opencodego"
	"github.com/AutumnsGrove/nova/provider/openrouter"
	"github.com/AutumnsGrove/nova/testkit"
)

// fakeSession is a scripted providerauth.Session. Wait blocks until finish is
// called or the context ends.
type fakeSession struct {
	mu        sync.Mutex
	submitted []string
	submitErr error
	closed    bool
	done      chan struct{}
	cred      providerauth.Credential
	err       error
}

func newFakeSession() *fakeSession { return &fakeSession{done: make(chan struct{})} }

func (f *fakeSession) Prompt() string { return "Paste the redirect URL" }
func (f *fakeSession) URL() string    { return "https://example.test/auth" }
func (f *fakeSession) Code() string   { return "ABCD-1234" }

func (f *fakeSession) Submit(input string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return f.submitErr
	}
	f.submitted = append(f.submitted, input)
	return nil
}

func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSession) Wait(ctx context.Context) (providerauth.Credential, error) {
	select {
	case <-ctx.Done():
		return providerauth.Credential{}, ctx.Err()
	case <-f.done:
		return f.cred, f.err
	}
}

// finish ends the flow with a credential or an error.
func (f *fakeSession) finish(c providerauth.Credential, err error) {
	f.cred, f.err = c, err
	close(f.done)
}

func (f *fakeSession) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// fakeStore backs a fake provider's Save, Load, and Delete in memory.
type fakeStore struct {
	mu      sync.Mutex
	cred    providerauth.Credential
	has     bool
	saveErr func(key string) error
}

func (s *fakeStore) save(_ string, c providerauth.Credential) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return "", s.saveErr(c.Key)
	}
	s.cred, s.has = c, true
	return "/fake/path", nil
}

func (s *fakeStore) load(string) (providerauth.Credential, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cred, s.has, nil
}

func (s *fakeStore) remove(string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cred, s.has = providerauth.Credential{}, false
	return nil
}

func (s *fakeStore) key() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cred.Key
}

// fakeProvider registers a provider with the given methods. The session handed
// to the next interactive Begin is returned through the channel, and the
// OpenBrowser seen by Begin is recorded.
type fakeProvider struct {
	id      string
	store   *fakeStore
	session *fakeSession
	browser func(string) error
	begun   chan struct{}
}

func registerFake(t *testing.T, id string, methods []providerauth.Method, envVars ...string) *fakeProvider {
	t.Helper()
	fp := &fakeProvider{id: id, store: &fakeStore{}, session: newFakeSession(), begun: make(chan struct{}, 4)}
	begin := func(_ context.Context, cfg providerauth.Config) (providerauth.Session, error) {
		fp.browser = cfg.OpenBrowser
		fp.begun <- struct{}{}
		return fp.session, nil
	}
	providerauth.Register(providerauth.Provider{
		ID:      id,
		Name:    "Fake " + id,
		Methods: methods,
		EnvVars: envVars,
		Begin: map[providerauth.Method]providerauth.BeginFunc{
			providerauth.OAuth:  begin,
			providerauth.Device: begin,
		},
		Save:   fp.store.save,
		Load:   fp.store.load,
		Delete: fp.store.remove,
	})
	return fp
}

// loginCmd builds a protocol command for a provider.
func loginCmd(typ, name string) proto.Command { return proto.Command{Type: typ, Name: name} }

// waitLoginEnd returns the next login_end event or fails after a deadline.
func waitLoginEnd(t *testing.T, events <-chan proto.Event) proto.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before login_end")
			}
			if ev.Type == proto.EventLoginEnd {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for login_end")
		}
	}
}

// noLoginEnd asserts no login_end arrives within a short real window. The
// window only bounds how long a negative test waits; positive paths never sleep.
func noLoginEnd(t *testing.T, events <-chan proto.Event) {
	t.Helper()
	select {
	case ev := <-events:
		if ev.Type == proto.EventLoginEnd {
			t.Fatalf("unexpected login_end: %+v", ev)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func decodeData(t *testing.T, resp proto.Response, v any) {
	t.Helper()
	if err := json.Unmarshal(resp.Data, v); err != nil {
		t.Fatalf("decode %s: %v", resp.Data, err)
	}
}

func wantCode(t *testing.T, resp proto.Response, code string) {
	t.Helper()
	if resp.OK || resp.Error == nil || resp.Error.Code != code {
		t.Fatalf("response = %+v, want error %s", resp, code)
	}
}

type listedProvider struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Methods      []string `json:"methods"`
	Ready        bool     `json:"ready"`
	Source       string   `json:"source"`
	Current      bool     `json:"current"`
	DefaultModel string   `json:"default_model"`
}

func listRows(t *testing.T, s *Server) map[string]listedProvider {
	t.Helper()
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdListProviders})
	if !resp.OK {
		t.Fatalf("list_providers = %+v", resp)
	}
	var rows []listedProvider
	decodeData(t, resp, &rows)
	out := map[string]listedProvider{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

func TestLogin_LGP01_ListProviders(t *testing.T) {
	registerFake(t, "lgp01", []providerauth.Method{providerauth.APIKey, providerauth.OAuth})
	s, _ := testServer(t)
	rows := listRows(t, s)
	r, ok := rows["lgp01"]
	if !ok || r.Ready || len(r.Methods) != 2 || r.Name != "Fake lgp01" {
		t.Fatalf("lgp01 row = %+v (found %v)", r, ok)
	}
	if _, ok := rows["openrouter"]; !ok {
		t.Fatal("the real openrouter provider is missing from the list")
	}
}

func TestLogin_LGP02_ListReflectsSavedKey(t *testing.T) {
	fp := registerFake(t, "lgp02", []providerauth.Method{providerauth.APIKey})
	s, _ := testServer(t)
	_, _ = fp.store.save("", providerauth.Credential{Method: providerauth.APIKey, Key: "k"})
	r := listRows(t, s)["lgp02"]
	if !r.Ready || r.Source != "store" {
		t.Fatalf("row = %+v, want ready from the store", r)
	}
}

func TestLogin_LGP03_UnknownProvider(t *testing.T) {
	s, _ := testServer(t)
	wantCode(t, s.Handle(context.Background(), loginCmd(proto.CmdLogin, "nope")), proto.CodeNotFound)
}

func TestLogin_LGP04_MissingName(t *testing.T) {
	s, _ := testServer(t)
	wantCode(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdLogin}), proto.CodeBadRequest)
}

func TestLogin_LGP05_APIKeySaves(t *testing.T) {
	fp := registerFake(t, "lgp05", []providerauth.Method{providerauth.APIKey})
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdLogin, Name: "lgp05", Text: "sk-secret"})
	if !resp.OK {
		t.Fatalf("login = %+v", resp)
	}
	var data struct {
		Provider string `json:"provider"`
		Saved    bool   `json:"saved"`
	}
	decodeData(t, resp, &data)
	if !data.Saved || data.Provider != "lgp05" || fp.store.key() != "sk-secret" {
		t.Fatalf("data = %+v key = %q", data, fp.store.key())
	}
}

func TestLogin_LGP06_APIKeyEmpty(t *testing.T) {
	fp := registerFake(t, "lgp06", []providerauth.Method{providerauth.APIKey})
	s, _ := testServer(t)
	wantCode(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdLogin, Name: "lgp06", Text: "  "}), proto.CodeBadRequest)
	if fp.store.key() != "" {
		t.Fatalf("an empty key was saved: %q", fp.store.key())
	}
}

func TestLogin_LGP07_MethodRequiredWhenAmbiguous(t *testing.T) {
	registerFake(t, "lgp07", []providerauth.Method{providerauth.APIKey, providerauth.OAuth})
	s, _ := testServer(t)
	wantCode(t, s.Handle(context.Background(), loginCmd(proto.CmdLogin, "lgp07")), proto.CodeBadRequest)
}

func TestLogin_LGP08_UnsupportedMethod(t *testing.T) {
	registerFake(t, "lgp08", []providerauth.Method{providerauth.APIKey})
	s, _ := testServer(t)
	cmd := proto.Command{Type: proto.CmdLogin, Name: "lgp08", Args: string(providerauth.Device)}
	wantCode(t, s.Handle(context.Background(), cmd), proto.CodeBadRequest)
}

// beginInteractive starts an interactive login and returns the response.
func beginInteractive(t *testing.T, s *Server, id string) proto.Response {
	t.Helper()
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdLogin, Name: id, Args: string(providerauth.OAuth)})
	if !resp.OK {
		t.Fatalf("login = %+v", resp)
	}
	return resp
}

func TestLogin_LGP09_InteractiveBegins(t *testing.T) {
	registerFake(t, "lgp09", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	events, cancel := s.Subscribe(16)
	defer cancel()
	resp := beginInteractive(t, s, "lgp09")
	var data struct {
		Provider, Method, URL, Code, Prompt string
	}
	decodeData(t, resp, &data)
	if data.URL != "https://example.test/auth" || data.Code != "ABCD-1234" || data.Prompt == "" || data.Method != "subscription" {
		t.Fatalf("data = %+v", data)
	}
	noLoginEnd(t, events)
}

func TestLogin_LGP10_NoBrowserOpened(t *testing.T) {
	fp := registerFake(t, "lgp10", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	beginInteractive(t, s, "lgp10")
	if fp.browser != nil {
		t.Fatal("a server must not hand Begin a browser opener")
	}
}

func TestLogin_LGP11_OneFlowPerProvider(t *testing.T) {
	registerFake(t, "lgp11", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	beginInteractive(t, s, "lgp11")
	cmd := proto.Command{Type: proto.CmdLogin, Name: "lgp11", Args: string(providerauth.OAuth)}
	wantCode(t, s.Handle(context.Background(), cmd), proto.CodeBusy)
}

func TestLogin_LGP12_FlowCompletes(t *testing.T) {
	fp := registerFake(t, "lgp12", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	events, cancel := s.Subscribe(16)
	defer cancel()
	beginInteractive(t, s, "lgp12")
	fp.session.finish(providerauth.Credential{Method: providerauth.OAuth, Key: "oauth-key"}, nil)
	ev := waitLoginEnd(t, events)
	if ev.ID != "lgp12" || ev.Error != nil || fp.store.key() != "oauth-key" {
		t.Fatalf("login_end = %+v key = %q", ev, fp.store.key())
	}
}

func TestLogin_LGP13_FlowFails(t *testing.T) {
	fp := registerFake(t, "lgp13", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	events, cancel := s.Subscribe(16)
	defer cancel()
	beginInteractive(t, s, "lgp13")
	fp.session.finish(providerauth.Credential{}, errors.New("user denied"))
	ev := waitLoginEnd(t, events)
	if ev.Error == nil || ev.Error.Code != proto.CodeProviderError || !strings.Contains(ev.Error.Message, "user denied") {
		t.Fatalf("login_end = %+v", ev)
	}
	if fp.store.key() != "" {
		t.Fatalf("a failed flow saved %q", fp.store.key())
	}
}

func TestLogin_LGP14_SubmitFeedsTheFlow(t *testing.T) {
	fp := registerFake(t, "lgp14", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	beginInteractive(t, s, "lgp14")
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdLoginSubmit, Name: "lgp14", Text: "http://localhost/cb?code=x"})
	if !resp.OK {
		t.Fatalf("login_submit = %+v", resp)
	}
	fp.session.mu.Lock()
	defer fp.session.mu.Unlock()
	if len(fp.session.submitted) != 1 || fp.session.submitted[0] != "http://localhost/cb?code=x" {
		t.Fatalf("submitted = %v", fp.session.submitted)
	}
}

func TestLogin_LGP15_SubmitRejectedKeepsFlowOpen(t *testing.T) {
	fp := registerFake(t, "lgp15", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	beginInteractive(t, s, "lgp15")
	fp.session.submitErr = errors.New("bad redirect")
	wantCode(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdLoginSubmit, Name: "lgp15", Text: "x"}), proto.CodeBadRequest)
	// The slot is still taken, so a retry can follow.
	cmd := proto.Command{Type: proto.CmdLogin, Name: "lgp15", Args: string(providerauth.OAuth)}
	wantCode(t, s.Handle(context.Background(), cmd), proto.CodeBusy)
}

func TestLogin_LGP16_SubmitWithNoFlow(t *testing.T) {
	registerFake(t, "lgp16", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	wantCode(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdLoginSubmit, Name: "lgp16", Text: "x"}), proto.CodeNotFound)
}

func TestLogin_LGP17_Cancel(t *testing.T) {
	fp := registerFake(t, "lgp17", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	events, cancel := s.Subscribe(16)
	defer cancel()
	beginInteractive(t, s, "lgp17")
	if resp := s.Handle(context.Background(), loginCmd(proto.CmdLoginCancel, "lgp17")); !resp.OK {
		t.Fatalf("login_cancel = %+v", resp)
	}
	if !fp.session.isClosed() {
		t.Fatal("cancel did not close the session")
	}
	noLoginEnd(t, events)
}

func TestLogin_LGP18_CancelWithNoFlow(t *testing.T) {
	registerFake(t, "lgp18", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	wantCode(t, s.Handle(context.Background(), loginCmd(proto.CmdLoginCancel, "lgp18")), proto.CodeNotFound)
}

func TestLogin_LGP19_LoginAgainAfterCancel(t *testing.T) {
	registerFake(t, "lgp19", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	beginInteractive(t, s, "lgp19")
	s.Handle(context.Background(), loginCmd(proto.CmdLoginCancel, "lgp19"))
	beginInteractive(t, s, "lgp19")
}

func TestLogin_LGP20_LogoutDeletes(t *testing.T) {
	fp := registerFake(t, "lgp20", []providerauth.Method{providerauth.APIKey})
	s, _ := testServer(t)
	_, _ = fp.store.save("", providerauth.Credential{Method: providerauth.APIKey, Key: "k"})
	resp := s.Handle(context.Background(), loginCmd(proto.CmdLogout, "lgp20"))
	if !resp.OK {
		t.Fatalf("logout = %+v", resp)
	}
	var data struct {
		Remaining string `json:"remaining"`
	}
	decodeData(t, resp, &data)
	if data.Remaining != "" || fp.store.key() != "" {
		t.Fatalf("remaining = %q key = %q", data.Remaining, fp.store.key())
	}
}

func TestLogin_LGP21_LogoutReportsRemainingSource(t *testing.T) {
	fp := registerFake(t, "lgp21", []providerauth.Method{providerauth.APIKey}, "LGP21_KEY")
	t.Setenv("LGP21_KEY", "from-env")
	s, _ := testServer(t)
	_, _ = fp.store.save("", providerauth.Credential{Method: providerauth.APIKey, Key: "k"})
	resp := s.Handle(context.Background(), loginCmd(proto.CmdLogout, "lgp21"))
	var data struct {
		Remaining string `json:"remaining"`
	}
	decodeData(t, resp, &data)
	if data.Remaining != "env" {
		t.Fatalf("remaining = %q, want env", data.Remaining)
	}
}

func TestLogin_LGP22_LogoutUnknown(t *testing.T) {
	s, _ := testServer(t)
	wantCode(t, s.Handle(context.Background(), loginCmd(proto.CmdLogout, "nope")), proto.CodeNotFound)
}

func TestLogin_LGP23_CloseCancelsFlows(t *testing.T) {
	testkit.NoLeaks(t)
	fp := registerFake(t, "lgp23", []providerauth.Method{providerauth.OAuth})
	s, _ := testServer(t)
	beginInteractive(t, s, "lgp23")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !fp.session.isClosed() {
		t.Fatal("Close left a login session open")
	}
}

func TestLogin_LGP24_NoSecretOnTheWire(t *testing.T) {
	const secret = "sk-very-secret-LGP24"
	fp := registerFake(t, "lgp24", []providerauth.Method{providerauth.APIKey})
	s, _ := testServer(t)
	events, cancel := s.Subscribe(32)
	defer cancel()

	var frames []string
	record := func(resp proto.Response) {
		raw, _ := json.Marshal(resp)
		frames = append(frames, string(raw))
	}
	cmd := proto.Command{Type: proto.CmdLogin, Name: "lgp24", Text: secret}
	first := s.Handle(context.Background(), cmd)
	record(first)
	// A failing save whose error text happens to contain the key.
	fp.store.saveErr = func(key string) error { return fmt.Errorf("cannot write %s to disk", key) }
	second := s.Handle(context.Background(), cmd)
	record(second)
	// Guard against a vacuous pass: the scan only means something once the
	// success and the failure both really happened.
	if !first.OK || second.OK || second.Error == nil || second.Error.Code != proto.CodeInternal {
		t.Fatalf("first = %+v second = %+v, want ok then an internal error", first, second)
	}
	record(s.Handle(context.Background(), loginCmd(proto.CmdListProviders, "")))

	cancel()
	for ev := range events {
		raw, _ := json.Marshal(ev)
		frames = append(frames, string(raw))
	}
	for _, f := range frames {
		if strings.Contains(f, secret) {
			t.Fatalf("the credential leaked into a frame: %s", f)
		}
	}
}

func FuzzLoginCommand(f *testing.F) {
	f.Add(proto.CmdLogin, "fuzzlogin", "", "key")
	f.Add(proto.CmdLogin, "fuzzlogin", "subscription", "")
	f.Add(proto.CmdLoginSubmit, "fuzzlogin", "", "http://localhost/cb")
	f.Add(proto.CmdLoginCancel, "", "", "")
	f.Add(proto.CmdLogout, "openrouter", "", "")
	f.Add(proto.CmdListProviders, "", "", "")
	known := map[string]bool{
		proto.CodeBadRequest: true, proto.CodeBusy: true, proto.CodeNotFound: true,
		proto.CodeProviderError: true, proto.CodeInternal: true, proto.CodeUnsupported: true,
	}
	f.Fuzz(func(t *testing.T, typ, name, args, text string) {
		registerFake(t, "fuzzlogin", []providerauth.Method{providerauth.APIKey, providerauth.OAuth})
		s, _ := testServer(t)
		switch typ {
		case proto.CmdLogin, proto.CmdLoginSubmit, proto.CmdLoginCancel, proto.CmdLogout, proto.CmdListProviders:
		default:
			t.Skip()
		}
		resp := s.Handle(context.Background(), proto.Command{Type: typ, Name: name, Args: args, Text: text})
		if resp.Type != proto.FrameResponse || resp.Command != typ {
			t.Fatalf("malformed response %+v", resp)
		}
		if !resp.OK && (resp.Error == nil || !known[resp.Error.Code]) {
			t.Fatalf("response %+v has no stable error code", resp)
		}
	})
}

// registerSequenced registers a provider whose every Begin hands out a fresh
// session, so a property test can open, cancel, and reopen flows.
func registerSequenced(t *testing.T, id string) *[]*fakeSession {
	t.Helper()
	var mu sync.Mutex
	sessions := &[]*fakeSession{}
	store := &fakeStore{}
	providerauth.Register(providerauth.Provider{
		ID: id, Name: id, Methods: []providerauth.Method{providerauth.OAuth},
		Begin: map[providerauth.Method]providerauth.BeginFunc{
			providerauth.OAuth: func(context.Context, providerauth.Config) (providerauth.Session, error) {
				mu.Lock()
				defer mu.Unlock()
				fs := newFakeSession()
				*sessions = append(*sessions, fs)
				return fs, nil
			},
		},
		Save: store.save, Load: store.load, Delete: store.remove,
	})
	return sessions
}

func TestProperty_LGP_OneLoginEnd(t *testing.T) {
	const id = "lgpprop"
	for seed := int64(1); seed <= 20; seed++ {
		sessions := registerSequenced(t, id)
		s, _ := testServer(t)
		events, cancel := s.Subscribe(256)
		rng := rand.New(rand.NewSource(seed))
		active := false
		for step := 0; step < 40; step++ {
			switch rng.Intn(4) {
			case 0:
				resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdLogin, Name: id})
				switch {
				case active:
					wantCode(t, resp, proto.CodeBusy)
				case !resp.OK:
					t.Fatalf("seed %d step %d: login = %+v", seed, step, resp)
				default:
					active = true
				}
			case 1:
				resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdLoginSubmit, Name: id, Text: "x"})
				if active != resp.OK {
					t.Fatalf("seed %d step %d: submit ok=%v with active=%v", seed, step, resp.OK, active)
				}
			case 2:
				resp := s.Handle(context.Background(), loginCmd(proto.CmdLoginCancel, id))
				if active != resp.OK {
					t.Fatalf("seed %d step %d: cancel ok=%v with active=%v", seed, step, resp.OK, active)
				}
				active = false
			case 3:
				if !active {
					continue
				}
				(*sessions)[len(*sessions)-1].finish(providerauth.Credential{Method: providerauth.OAuth, Key: "k"}, nil)
				if ev := waitLoginEnd(t, events); ev.ID != id || ev.Error != nil {
					t.Fatalf("seed %d: login_end = %+v", seed, ev)
				}
				active = false
			}
		}
		// Whatever was cancelled or left open must not have produced a login_end.
		noLoginEnd(t, events)
		cancel()
		_ = s.Close()
	}
}

func TestLogin_LGP26_ListHonoursConfigKey(t *testing.T) {
	s, _ := testServer(t)
	s.cfg.Provider.OpenCodeGo.APIKey = "from-config"
	r := listRows(t, s)["opencode-go"]
	if !r.Ready || r.Source != "config" {
		t.Fatalf("opencode-go row = %+v, want ready from config", r)
	}
}

func TestLogin_LGP27_ListMarksTheCurrentProvider(t *testing.T) {
	s, _ := testServer(t)
	s.cfg.Model = opencodego.DefaultModel
	rows := listRows(t, s)
	if !rows["opencode-go"].Current || rows["openrouter"].Current {
		t.Fatalf("current flags = opencode-go:%v openrouter:%v", rows["opencode-go"].Current, rows["openrouter"].Current)
	}
	// A model the catalog does not know is served by OpenRouter, as the
	// request router would route it.
	s.cfg.Model = "nobody/unknown"
	rows = listRows(t, s)
	if !rows["openrouter"].Current || rows["opencode-go"].Current {
		t.Fatalf("unknown model: openrouter current=%v opencode-go current=%v", rows["openrouter"].Current, rows["opencode-go"].Current)
	}
}

func TestLogin_LGP28_ListCarriesTheDefaultModel(t *testing.T) {
	s, _ := testServer(t)
	rows := listRows(t, s)
	if rows["openrouter"].DefaultModel != openrouter.DefaultModel || rows["opencode-go"].DefaultModel != opencodego.DefaultModel {
		t.Fatalf("default models = %q, %q", rows["openrouter"].DefaultModel, rows["opencode-go"].DefaultModel)
	}
}
