package bundled

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/config"
)

func TestBundled_BND01_AllListsFive(t *testing.T) {
	all := All()
	var names []string
	for _, b := range all {
		names = append(names, b.Name())
	}
	if got := strings.Join(names, ","); got != "tasks,ask,plan,subagent,webfetch" {
		t.Fatalf("All() = %q", got)
	}
}

func TestBundled_BND02_EnabledHonoursConfig(t *testing.T) {
	cfg := &config.Config{Extensions: config.ExtensionsConfig{Disable: []string{"tasks", "webfetch"}}}
	var names []string
	for _, b := range Enabled(cfg) {
		names = append(names, b.Name())
	}
	if got := strings.Join(names, ","); got != "ask,plan,subagent" {
		t.Fatalf("Enabled() = %q", got)
	}
	if len(Enabled(nil)) != 5 {
		t.Fatalf("Enabled(nil) = %d, want 5", len(Enabled(nil)))
	}
}
