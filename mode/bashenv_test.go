package mode

import (
	"context"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/tool"
)

// bashSees runs `echo` over the given variable in the built-in bash tool.
func bashSees(t *testing.T, cfg *config.Config, name string) string {
	t.Helper()
	_, all, err := buildTools(cfg)
	if err != nil {
		t.Fatalf("buildTools: %v", err)
	}
	res, err := all["bash"].Run(context.Background(), []byte(`{"command":"echo \"[$`+name+`]\""}`), &tool.Context{WorkDir: t.TempDir()})
	if err != nil || res.IsError {
		t.Fatalf("bash = %+v, %v", res, err)
	}
	return strings.TrimSpace(res.Content)
}

// A model-run command must not read the provider keys Nova itself uses.
func TestBuildTools_BashDoesNotInheritNovaCredentials(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-secret")
	t.Setenv("OPENCODE_GO_API_KEY", "go-secret")
	t.Setenv("NOVA_SERVE_TOKEN", "serve-secret")
	t.Setenv("MY_OWN_TOKEN", "mine")
	cfg := config.Defaults()
	for _, name := range []string{"OPENROUTER_API_KEY", "OPENCODE_GO_API_KEY", "NOVA_SERVE_TOKEN"} {
		if got := bashSees(t, &cfg, name); got != "[]" {
			t.Errorf("%s visible to bash: %q", name, got)
		}
	}
	// Only Nova's own credentials are scrubbed by default, so ordinary tools that
	// the user's own variables configure keep working.
	if got := bashSees(t, &cfg, "MY_OWN_TOKEN"); got != "[mine]" {
		t.Errorf("MY_OWN_TOKEN = %q, want it kept", got)
	}
}

func TestBuildTools_BashScrubEnvAddsNamesAndInheritCredentialsOptsOut(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-secret")
	t.Setenv("MY_OWN_TOKEN", "mine")
	cfg := config.Defaults()
	cfg.Tools.Bash.ScrubEnv = []string{"MY_OWN_TOKEN"}
	if got := bashSees(t, &cfg, "MY_OWN_TOKEN"); got != "[]" {
		t.Errorf("a configured name is visible: %q", got)
	}
	cfg.Tools.Bash.InheritCredentials = true
	if got := bashSees(t, &cfg, "OPENROUTER_API_KEY"); got != "[sk-or-secret]" {
		t.Errorf("inherit_credentials did not restore the key: %q", got)
	}
	if got := bashSees(t, &cfg, "MY_OWN_TOKEN"); got != "[]" {
		t.Errorf("a configured name is visible with inherit_credentials: %q", got)
	}
}
