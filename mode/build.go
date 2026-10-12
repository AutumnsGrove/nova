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
	Out         io.Writer
	In          io.Reader

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
		Out: o.Out, In: o.In, NoSession: o.NoSession, Session: o.Session,
		Tools: o.Tools, NoTools: o.NoTools, Interactive: o.Interactive,
		ExtraTools: o.ExtraTools, SystemPrompt: o.SystemPrompt,
	})
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
