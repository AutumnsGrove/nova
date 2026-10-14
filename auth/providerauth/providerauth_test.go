package providerauth

import (
	"context"
	"os"
	"testing"
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
