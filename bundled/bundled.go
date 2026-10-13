// Package bundled collects the extensions Nova includes by default. Each is an
// in-tree Go package that registers through the same host path as a subprocess
// extension.
package bundled

import (
	"github.com/AutumnsGrove/nova/bundled/ask"
	"github.com/AutumnsGrove/nova/bundled/plan"
	"github.com/AutumnsGrove/nova/bundled/subagent"
	"github.com/AutumnsGrove/nova/bundled/tasks"
	"github.com/AutumnsGrove/nova/bundled/webfetch"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/ext"
)

// All returns a fresh instance of every bundled extension, in a stable order.
func All() []ext.Bundled {
	return []ext.Bundled{
		tasks.New(),
		ask.New(),
		plan.New(),
		subagent.New(),
		webfetch.New(),
	}
}

// Enabled returns the bundled extensions minus the names in
// config.extensions.disable.
func Enabled(cfg *config.Config) []ext.Bundled {
	disabled := map[string]bool{}
	if cfg != nil {
		for _, name := range cfg.Extensions.Disable {
			disabled[name] = true
		}
	}
	var out []ext.Bundled
	for _, b := range All() {
		if !disabled[b.Name()] {
			out = append(out, b)
		}
	}
	return out
}
