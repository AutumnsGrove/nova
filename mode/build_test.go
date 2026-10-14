package mode

import (
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/permission"
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
