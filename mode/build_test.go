package mode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/provider/opencodego"
	"github.com/AutumnsGrove/nova/tool"
)

// TestBuild_PERM01_InteractiveDefaultsToAllow checks the TUI does not prompt
// for every destructive tool when the config says nothing.
func TestBuild_PERM01_InteractiveDefaultsToAllow(t *testing.T) {
	cfg := &config.Config{}
	applyInteractiveDefault(cfg, true, false)
	if got := cfg.Permissions.Default; got != permission.Allow {
		t.Errorf("Default = %q, want allow", got)
	}
}

// TestBuild_PERM02_ExplicitDefaultWins checks a configured ask or deny survives.
func TestBuild_PERM02_ExplicitDefaultWins(t *testing.T) {
	for _, d := range []permission.Action{permission.Ask, permission.Deny} {
		cfg := &config.Config{Permissions: permission.Policy{Default: d}}
		applyInteractiveDefault(cfg, true, false)
		if cfg.Permissions.Default != d {
			t.Errorf("Default = %q, want %q kept", cfg.Permissions.Default, d)
		}
	}
}

// TestBuild_PERM03_YesOverridesAsk checks --yes beats a configured ask in the TUI.
func TestBuild_PERM03_YesOverridesAsk(t *testing.T) {
	cfg := &config.Config{Permissions: permission.Policy{Default: permission.Ask}}
	applyInteractiveDefault(cfg, true, true)
	if cfg.Permissions.Default != permission.Allow {
		t.Errorf("Default = %q, want allow", cfg.Permissions.Default)
	}
}

// TestBuild_PERM04_HeadlessUnchanged checks non-interactive runs keep the
// annotation-based default.
func TestBuild_PERM04_HeadlessUnchanged(t *testing.T) {
	cfg := &config.Config{}
	applyInteractiveDefault(cfg, false, true)
	if cfg.Permissions.Default != "" {
		t.Errorf("Default = %q, want empty", cfg.Permissions.Default)
	}
}

// DenyAll is the action's policy: the tools the build registered are the run's
// whole surface, and denying them too left the review with nothing it could
// call -- every tool call was refused and the action posted nothing. Both halves
// matter: the filtered tool names and the tools added on the side.
func TestBuild_PERM05_DenyAllAllowsTheRegisteredTools(t *testing.T) {
	extra := tool.New(tool.Info{Name: "review_summary", Description: "x"}, nil)
	cfg, _, err := loadConfig(BuildOptions{
		DenyAll:    true,
		Tools:      []string{"read", "grep"},
		ExtraTools: []tool.Tool{extra},
	}, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	for name, want := range map[string]permission.Action{
		"read":           permission.Allow,
		"grep":           permission.Allow,
		"review_summary": permission.Allow,
		"bash":           permission.Deny,
		"write":          permission.Deny,
	} {
		if got := cfg.Permissions.Decide(permission.Call{Tool: name}, false).Action; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// Whether a review can run is a property of the model it was asked for, and the
// key can come from any of the three places the request authenticates from: the
// config, the environment, and the store /login writes. Asking the environment
// alone skipped a review whose key was in the other two, and an unset --model is
// the config's model, not "the default provider".
func TestBuild_HasKeyForModel(t *testing.T) {
	const goModel = "opencode-go/deepseek-v4.1-flash"
	const orModel = "deepseek/deepseek-v4.1"
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENCODE_GO_API_KEY", "")

	for _, tc := range []struct {
		name  string
		cfg   *config.Config
		env   map[string]string
		store string
		want  bool
	}{
		{name: "nothing anywhere", cfg: &config.Config{Model: goModel}},
		{name: "environment key", cfg: &config.Config{Model: goModel},
			env: map[string]string{"OPENCODE_GO_API_KEY": "go-key"}, want: true},
		{name: "config key", cfg: &config.Config{Model: goModel,
			Provider: config.ProviderConfig{OpenCodeGo: config.OpenCodeGoConfig{APIKey: "cfg-key"}}}, want: true},
		{name: "stored key", cfg: &config.Config{Model: goModel}, store: "stored-key", want: true},
		{name: "an opencode-go key is not an OpenRouter key", cfg: &config.Config{Model: orModel},
			env: map[string]string{"OPENCODE_GO_API_KEY": "go-key"}},
		{name: "OpenRouter key for an OpenRouter model", cfg: &config.Config{Model: orModel},
			env: map[string]string{"OPENROUTER_API_KEY": "or-key"}, want: true},
		{name: "OpenRouter key for the default model", cfg: &config.Config{Model: ""},
			env: map[string]string{"OPENROUTER_API_KEY": "or-key"}, want: true},
		{name: "an opencode-go key does not run the default model", cfg: &config.Config{Model: ""},
			env: map[string]string{"OPENCODE_GO_API_KEY": "go-key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if tc.store != "" {
				if err := opencode.SaveKey(opencode.DefaultKeyPath(home), tc.store); err != nil {
					t.Fatal(err)
				}
			}
			if got := HasKeyForModel(tc.cfg, home); got != tc.want {
				t.Fatalf("HasKeyForModel = %t, want %t", got, tc.want)
			}
		})
	}
}

// LoadConfig is the cheap half of Build: it settles the model the way a build
// would -- the flag, then the config -- and hands back the home a stored key is
// read from, so the caller can decide whether to build at all.
func TestBuild_LoadConfigSettlesTheModelWithoutBuilding(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "nova")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: from/config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, gotHome, err := LoadConfig(BuildOptions{Home: home, CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Model != "from/config" {
		t.Errorf("Model = %q, want the config's model", cfg.Model)
	}
	if gotHome != home {
		t.Errorf("home = %q, want %q", gotHome, home)
	}
	cfg, _, err = LoadConfig(BuildOptions{Home: home, CWD: t.TempDir(), Model: "from/flag"})
	if err != nil {
		t.Fatalf("LoadConfig with a flag: %v", err)
	}
	if cfg.Model != "from/flag" {
		t.Errorf("Model = %q, want the flag to win", cfg.Model)
	}
}

// Live discovery registers OpenCode Go models the baseline catalog does not
// carry, and the provider build loads that overlay before the request resolves
// the model. A decision taken without it reads a discovered model as unknown,
// falls through to the OpenRouter branch, and skips for lack of an OpenRouter
// key -- the same false skip, one layer down (#146).
func TestBuild_HasKeyForModelLiveDiscoveredModel(t *testing.T) {
	const liveModel = "opencode-go/live-only"
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENCODE_GO_API_KEY", "")
	home := t.TempDir()
	if err := opencode.SaveKey(opencode.DefaultKeyPath(home), "stored-go-key"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "nova")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := `{"checked_at":0,"models":[{"id":"` + liveModel + `","name":"Live Only","slug":"live-only","context_window":1000,"dialect":"chat_completions"}]}`
	if err := os.WriteFile(filepath.Join(dir, "opencode-go-models.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	// The catalog is process-wide, so put it back for every other test.
	t.Cleanup(func() {
		empty := filepath.Join(t.TempDir(), "models.json")
		if err := os.WriteFile(empty, []byte(`{"models":[]}`), 0o644); err != nil {
			t.Error(err)
			return
		}
		_ = opencodego.LoadCache(empty)
	})

	if got := HasKeyForModel(&config.Config{Model: liveModel}, home); !got {
		t.Fatalf("HasKeyForModel(%q) = false with the OpenCode Go key stored", liveModel)
	}
}
