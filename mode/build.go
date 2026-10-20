package mode

import (
	"fmt"
	"io"
	"os"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/tool"
)

// BuildOptions describes a server assembled from the on-disk config. CWD and
// Home default to the process working directory and home.
type BuildOptions struct {
	CWD, Home   string
	Mode        string
	Version     string
	Model       string
	MaxTurns    int
	Tools       []string
	NoTools     bool
	NoSession   bool
	Session     string
	Interactive bool
	Flags       map[string]any
	Yes         bool
	Stderr      io.Writer

	ExtraTools   []tool.Tool
	SystemPrompt string
	DenyAll      bool
}

// Build loads the config for CWD, applies the flags, and builds a server.
func Build(o BuildOptions) (*Server, error) {
	cwd, home := o.CWD, o.Home
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	trust := config.NewTrustStore(trustPath(home))
	cfg, warnings, err := config.Load(config.LoadOptions{CWD: cwd, Home: home, Trusted: trust.IsTrusted, Flags: o.Flags})
	if err != nil {
		return nil, err
	}
	if o.Stderr != nil {
		for _, w := range warnings {
			fmt.Fprintf(o.Stderr, "warning: %s: %s\n", w.Source, w.Message)
		}
	}
	applyInteractiveDefault(cfg, o.Interactive, o.Yes)
	if o.Yes {
		cfg.Permissions.Headless = permission.Allow
		if o.Stderr != nil {
			fmt.Fprintln(o.Stderr, "warning: --yes auto-approves tool calls for this run")
		}
	}
	if o.Model != "" {
		cfg.Model = o.Model
	}
	if o.MaxTurns > 0 {
		cfg.MaxTurns = o.MaxTurns
	}
	if o.DenyAll {
		cfg.Permissions = permission.Policy{Default: permission.Deny, Headless: permission.Deny}
	}
	return New(Options{
		Config: cfg, CWD: cwd, Home: home, Version: o.Version, Mode: o.Mode,
		NoSession: o.NoSession, Session: o.Session,
		Tools: o.Tools, NoTools: o.NoTools,
		ExtraTools: o.ExtraTools, SystemPrompt: o.SystemPrompt,
	})
}

// applyInteractiveDefault makes the interactive TUI run tools without asking,
// as pi does, because bash, write, and edit are all annotated destructive and
// would otherwise prompt on every call. An explicit permissions.default in the
// config (for example "ask") wins, and --yes forces allow over an unset or "ask"
// default. Headless modes are untouched: there an Ask still becomes Headless.
func applyInteractiveDefault(cfg *config.Config, interactive, yes bool) {
	if !interactive {
		return
	}
	d := cfg.Permissions.Default
	if d == "" || (yes && d == permission.Ask) {
		cfg.Permissions.Default = permission.Allow
	}
}

// HasProviderKey reports whether an OpenRouter key is available. An empty home
// means the user's home directory.
func HasProviderKey(home string) bool {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	key, _, err := auth.Resolve("", os.Getenv, auth.NewFileStore(keyPath(home)))
	return err == nil && key != ""
}
