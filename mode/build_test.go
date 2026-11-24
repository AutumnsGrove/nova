package mode

import (
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/permission"
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

// Whether a review can run is a property of the model it was asked for, not of
// one environment variable: the action asked for an OpenRouter key and skipped
// green on a repository whose working key was an opencode-go one.
func TestBuild_HasKeyForModel(t *testing.T) {
	const goModel = "opencode-go/deepseek-v4.1-flash"
	const orModel = "deepseek/deepseek-v4.1"
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENCODE_GO_API_KEY", "")
	home := t.TempDir()
	if HasKeyForModel(goModel, home) {
		t.Fatal("an opencode-go model reported a key with none set")
	}

	t.Setenv("OPENCODE_GO_API_KEY", "go-key")
	if !HasKeyForModel(goModel, home) {
		t.Fatal("the opencode-go key did not count for an opencode-go model")
	}
	if HasKeyForModel(orModel, home) {
		t.Fatal("the opencode-go key counted for an OpenRouter model")
	}

	t.Setenv("OPENROUTER_API_KEY", "or-key")
	if !HasKeyForModel(orModel, home) {
		t.Fatal("the OpenRouter key did not count for an OpenRouter model")
	}
	if !HasKeyForModel("", home) {
		t.Fatal("the default model no longer resolves its key")
	}
}
