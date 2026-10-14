package providerauth

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider/opencodego"
	"github.com/AutumnsGrove/nova/provider/openrouter"
)

func TestProviderauth_SGN01_RegistryHasBoth(t *testing.T) {
	or, ok := Get("openrouter")
	if !ok || or.Name != "OpenRouter" {
		t.Fatalf("openrouter: ok=%v name=%q", ok, or.Name)
	}
	go_, ok := Get("opencode-go")
	if !ok || go_.Name != "OpenCode Go" {
		t.Fatalf("opencode-go: ok=%v name=%q", ok, go_.Name)
	}
	if len(All()) < 2 {
		t.Fatalf("All() = %d providers", len(All()))
	}
}

func TestProviderauth_SGN02_Unknown(t *testing.T) {
	if _, ok := Get("nope"); ok {
		t.Fatal("Get found an unregistered provider")
	}
}

func TestProviderauth_SGN03_DuplicateReplaces(t *testing.T) {
	const id = "fake-dup-c22"
	Register(Provider{ID: id, Name: "first"})
	Register(Provider{ID: id, Name: "second"})
	n, name := 0, ""
	for _, p := range All() {
		if p.ID == id {
			n++
			name = p.Name
		}
	}
	if n != 1 || name != "second" {
		t.Fatalf("duplicate id gave %d entries named %q", n, name)
	}
}

func TestProviderauth_SGN04_NewProviderFileIsPickedUp(t *testing.T) {
	const id = "fake-new-c22"
	Register(Provider{ID: id, Name: "Fake", Methods: []Method{APIKey}})
	p, ok := Get(id)
	if !ok || len(p.Methods) != 1 {
		t.Fatalf("new provider not registered: ok=%v p=%+v", ok, p)
	}
}

func TestProviderauth_SGN05_ConfigFirst(t *testing.T) {
	p, _ := Get("openrouter")
	s := Resolve(p, "cfg", func(string) string { return "env" }, t.TempDir())
	if !s.Ready || s.Source != "config" {
		t.Fatalf("status = %+v", s)
	}
}

func TestProviderauth_SGN06_EnvSecond(t *testing.T) {
	p, _ := Get("openrouter")
	s := Resolve(p, "", func(string) string { return "env" }, t.TempDir())
	if !s.Ready || s.Source != "env" {
		t.Fatalf("status = %+v", s)
	}
}

func TestProviderauth_SGN07_StoreThird(t *testing.T) {
	home := t.TempDir()
	p, _ := Get("openrouter")
	if _, err := p.Save(home, Credential{Method: APIKey, Key: "stored"}); err != nil {
		t.Fatal(err)
	}
	s := Resolve(p, "", func(string) string { return "" }, home)
	if !s.Ready || s.Source != "store" {
		t.Fatalf("status = %+v", s)
	}
}

func TestProviderauth_SGN08_NothingSet(t *testing.T) {
	p, _ := Get("openrouter")
	s := Resolve(p, "", func(string) string { return "  " }, t.TempDir())
	if s.Ready || s.Source != "" {
		t.Fatalf("status = %+v", s)
	}
}

func TestProviderauth_SGN09_ResolveIsNotCached(t *testing.T) {
	home := t.TempDir()
	p, _ := Get("openrouter")
	if s := Resolve(p, "", func(string) string { return "" }, home); s.Ready {
		t.Fatal("ready before a key was saved")
	}
	if _, err := p.Save(home, Credential{Method: APIKey, Key: "later"}); err != nil {
		t.Fatal(err)
	}
	if s := Resolve(p, "", func(string) string { return "" }, home); !s.Ready || s.Source != "store" {
		t.Fatalf("status after save = %+v", s)
	}
}

func TestProviderauth_SGN21_StoreRoundTripsAt0600(t *testing.T) {
	home := t.TempDir()
	p, _ := Get("opencode-go")
	path, err := p.Save(home, Credential{Method: Device, Key: "go-key"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("store mode = %04o, want 0600", perm)
	}
	c, ok, err := p.Load(home)
	if err != nil || !ok || c.Key != "go-key" {
		t.Fatalf("Load = (%+v, %v, %v)", c, ok, err)
	}
}

func TestProviderauth_SGN24_BrowserOpens(t *testing.T) {
	p, _ := Get("openrouter")
	var got string
	session, err := p.Begin[OAuth](context.Background(), Config{
		Home:        t.TempDir(),
		OpenBrowser: func(u string) error { got = u; return nil },
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer session.Close()
	if got == "" || got != session.URL() {
		t.Fatalf("browser opened %q, URL %q", got, session.URL())
	}
}

func TestProperty_SGN_Registry(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	ids := map[string]bool{}
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("prop-%d-c22", r.Intn(100000))
		Register(Provider{ID: id, Name: id})
		ids[id] = true
	}
	counts := map[string]int{}
	for _, p := range All() {
		counts[p.ID]++
	}
	for id := range ids {
		if counts[id] != 1 {
			t.Fatalf("%s appears %d times", id, counts[id])
		}
		if _, ok := Get(id); !ok {
			t.Fatalf("Get(%s) missed", id)
		}
	}
}

func TestProviderauth_SGN39_FakeProviderNeedsNoNetwork(t *testing.T) {
	home := t.TempDir()
	Register(Provider{
		ID: "fake-offline-c22", Name: "Offline", Methods: []Method{APIKey},
		Begin: map[Method]BeginFunc{
			APIKey: func(context.Context, Config) (Session, error) { return newKeySession("Paste an offline key"), nil },
		},
		Save: func(string, Credential) (string, error) { return home, nil },
	})
	p, ok := Get("fake-offline-c22")
	if !ok {
		t.Fatal("fake provider not registered")
	}
	session, err := p.Begin[APIKey](context.Background(), Config{Home: home})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	done := make(chan Credential, 1)
	go func() {
		c, _ := session.Wait(context.Background())
		done <- c
	}()
	if err := session.Submit("offline-key"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if c := <-done; c.Key != "offline-key" || c.Method != APIKey {
		t.Fatalf("credential = %+v", c)
	}
	if path, err := p.Save(home, Credential{Method: APIKey, Key: "offline-key"}); err != nil || path != home {
		t.Fatalf("Save = (%q, %v)", path, err)
	}
}

func TestProviderauth_SGN41_ForModelOpenRouter(t *testing.T) {
	p, ok := ForModel(openrouter.DefaultModel)
	if !ok || p.ID != "openrouter" {
		t.Fatalf("ForModel(openrouter default) = %q, %v", p.ID, ok)
	}
}

func TestProviderauth_SGN42_ForModelOpenCodeGo(t *testing.T) {
	// The catalog calls this provider "opencodego" and the sign-in registry
	// calls it "opencode-go"; ForModel is what bridges the two names.
	p, ok := ForModel(opencodego.DefaultModel)
	if !ok || p.ID != "opencode-go" {
		t.Fatalf("ForModel(opencode-go default) = %q, %v", p.ID, ok)
	}
}

func TestProviderauth_SGN43_ForModelUnknownFallsBackToOpenRouter(t *testing.T) {
	// The request router sends an unknown model id to OpenRouter, so the
	// sign-in that matters for it is OpenRouter's.
	p, ok := ForModel("nobody/never-heard-of-it")
	if !ok || p.ID != "openrouter" {
		t.Fatalf("ForModel(unknown) = %q, %v", p.ID, ok)
	}
}

func TestProviderauth_SGN44_ConfigKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.APIKey = "cfg-key"
	goP, _ := Get("opencode-go")
	if got := goP.ConfigKeyFrom(&cfg); got != "cfg-key" {
		t.Fatalf("opencode-go config key = %q", got)
	}
	orP, _ := Get("openrouter")
	if got := orP.ConfigKeyFrom(&cfg); got != "" {
		t.Fatalf("openrouter has no config key, got %q", got)
	}
	if got := goP.ConfigKeyFrom(nil); got != "" {
		t.Fatalf("nil config gave %q", got)
	}
}

func TestProviderauth_SGN58_OAuthSessionReportsStatus(t *testing.T) {
	flow, raw, err := auth.Begin(auth.LoginOptions{AuthURL: "https://example.test/auth", Out: io.Discard})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer flow.Close()
	var sess Session = &oauthSession{flow: flow, url: raw, name: "OpenRouter"}
	reporter, ok := sess.(StatusReporter)
	if !ok {
		t.Fatal("the OAuth session does not report its status")
	}
	waiting := reporter.Status()
	if !strings.Contains(waiting, flow.Addr()) || !strings.Contains(strings.ToLower(waiting), "waiting") {
		t.Fatalf("waiting status = %q, want it to name %s", waiting, flow.Addr())
	}
	u, _ := url.Parse(raw)
	redirect := u.Query().Get("callback_url") + "?code=c&state=" + url.QueryEscape(u.Query().Get("state"))
	if err := sess.Submit(redirect); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := reporter.Status(); got == waiting || !strings.Contains(strings.ToLower(got), "received") {
		t.Fatalf("status after the redirect = %q, want a received message different from %q", got, waiting)
	}
}
