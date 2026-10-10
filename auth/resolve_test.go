package auth

import (
	"errors"
	"testing"
)

// fakeStore is a Store backed by a fixed key or error.
type fakeStore struct {
	key string
	err error
}

func (s *fakeStore) Load() (string, error) { return s.key, s.err }
func (s *fakeStore) Save(string) error     { return nil }
func (s *fakeStore) Delete() error         { return nil }

// envMap turns a map into the lookup function Resolve expects.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestResolve_RES01_ConfigWins checks config beats env and store.
func TestResolve_RES01_ConfigWins(t *testing.T) {
	key, source, err := Resolve("cfg", envMap(map[string]string{EnvAPIKey: "env"}), &fakeStore{key: "store"})
	if err != nil || key != "cfg" || source != "config" {
		t.Fatalf("Resolve() = (%q, %q, %v), want (cfg, config, nil)", key, source, err)
	}
}

// TestResolve_RES02_EnvOverStore checks env beats store.
func TestResolve_RES02_EnvOverStore(t *testing.T) {
	key, source, err := Resolve("", envMap(map[string]string{EnvAPIKey: "env"}), &fakeStore{key: "store"})
	if err != nil || key != "env" || source != "env" {
		t.Fatalf("Resolve() = (%q, %q, %v), want (env, env, nil)", key, source, err)
	}
}

// TestResolve_RES03_StoreOnly checks the store is the last resort.
func TestResolve_RES03_StoreOnly(t *testing.T) {
	key, source, err := Resolve("", envMap(nil), &fakeStore{key: "store"})
	if err != nil || key != "store" || source != "store" {
		t.Fatalf("Resolve() = (%q, %q, %v), want (store, store, nil)", key, source, err)
	}
}

// TestResolve_RES04_None checks no credential is ErrNoKey.
func TestResolve_RES04_None(t *testing.T) {
	_, _, err := Resolve("", envMap(nil), &fakeStore{err: ErrNoKey})
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("Resolve() error = %v, want ErrNoKey", err)
	}
}

// TestResolve_RES05_Whitespace checks keys are trimmed and a whitespace-only
// environment value falls through.
func TestResolve_RES05_Whitespace(t *testing.T) {
	key, source, err := Resolve("  k\n", nil, nil)
	if err != nil || key != "k" || source != "config" {
		t.Fatalf("config trim = (%q, %q, %v)", key, source, err)
	}
	key, source, err = Resolve("", envMap(map[string]string{EnvAPIKey: "   "}), &fakeStore{key: "  from-store "})
	if err != nil || key != "from-store" || source != "store" {
		t.Fatalf("whitespace env = (%q, %q, %v), want (from-store, store, nil)", key, source, err)
	}
}

// TestResolve_RES06_SourceNames checks each source is reported by name.
func TestResolve_RES06_SourceNames(t *testing.T) {
	cases := []struct {
		name string
		key  string
		env  map[string]string
		st   *fakeStore
		want string
	}{
		{"config", "c", map[string]string{EnvAPIKey: "e"}, &fakeStore{key: "s"}, "config"},
		{"env", "", map[string]string{EnvAPIKey: "e"}, &fakeStore{key: "s"}, "env"},
		{"store", "", nil, &fakeStore{key: "s"}, "store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, source, err := Resolve(tc.key, envMap(tc.env), tc.st)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if source != tc.want {
				t.Fatalf("source = %q, want %q", source, tc.want)
			}
		})
	}
}
