package mode

import (
	"fmt"
	"io"
	"os"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/mode/providers"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/opencodego"
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
	// DenyAll allows each tool this build registered and denies every other
	// one, which is the action's whole surface.
	DenyAll bool
}

// Build loads the config for CWD, applies the flags, and builds a server. The
// server can re-read the config on a reload, with the same flags applied again.
func Build(o BuildOptions) (*Server, error) {
	cwd, home := dirs(o)
	load := func() (*config.Config, []string, error) { return loadConfig(o, cwd, home) }
	cfg, warnings, err := load()
	if err != nil {
		return nil, err
	}
	if o.Stderr != nil {
		for _, w := range warnings {
			fmt.Fprintf(o.Stderr, "warning: %s\n", w)
		}
		if o.Yes {
			fmt.Fprintln(o.Stderr, "warning: --yes auto-approves tool calls for this run")
		}
	}
	return New(Options{
		Config: cfg, Reload: load, CWD: cwd, Home: home, Version: o.Version, Mode: o.Mode,
		NoSession: o.NoSession, Session: o.Session,
		Tools: o.Tools, NoTools: o.NoTools,
		ExtraTools: o.ExtraTools, SystemPrompt: o.SystemPrompt,
	})
}

// dirs settles the working directory and home a build reads from.
func dirs(o BuildOptions) (cwd, home string) {
	cwd, home = o.CWD, o.Home
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return cwd, home
}

// LoadConfig reads the config a build would use, with the flags applied, and
// constructs nothing: no session, no tool registry, no extension host, no MCP
// server. A caller that asks a question of the config -- "do I have a key for
// this model?" -- has to be able to answer it without paying for a server it
// may never use, because building one starts subprocesses and closing it waits
// out their handshakes. Home comes back with the config because that is where a
// stored key lives. Warnings are left to Build, which prints them.
func LoadConfig(o BuildOptions) (cfg *config.Config, home string, err error) {
	cwd, home := dirs(o)
	cfg, _, err = loadConfig(o, cwd, home)
	if err != nil {
		return nil, "", err
	}
	return cfg, home, nil
}

// loadConfig reads the config for cwd and applies the command-line flags on top
// of it. The warnings come back as text, "source: message".
func loadConfig(o BuildOptions, cwd, home string) (*config.Config, []string, error) {
	trust := config.NewTrustStore(trustPath(home))
	cfg, found, err := config.Load(config.LoadOptions{CWD: cwd, Home: home, Trusted: trust.IsTrusted, Flags: o.Flags})
	if err != nil {
		return nil, nil, err
	}
	warnings := make([]string, len(found))
	for i, w := range found {
		warnings[i] = w.Source + ": " + w.Message
	}
	applyInteractiveDefault(cfg, o.Interactive, o.Yes)
	if o.Yes {
		cfg.Permissions.Headless = permission.Allow
	}
	if o.Model != "" {
		cfg.Model = o.Model
	}
	if o.MaxTurns > 0 {
		cfg.MaxTurns = o.MaxTurns
	}
	if o.DenyAll {
		cfg.Permissions = permission.Policy{Default: permission.Deny, Headless: permission.Deny}
		// Deny everything the build did not register. Denying the registered
		// tools too leaves the agent with nothing it may call, so the review
		// run made its tool calls, was refused, and posted nothing.
		for _, name := range o.Tools {
			cfg.Permissions.Rules = append(cfg.Permissions.Rules, permission.Rule{Tool: name, Action: permission.Allow})
		}
		for _, t := range o.ExtraTools {
			cfg.Permissions.Rules = append(cfg.Permissions.Rules, permission.Rule{Tool: t.Info().Name, Action: permission.Allow})
		}
	}
	return cfg, warnings, nil
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

// HasKeyForModel reports whether a key is available for the configured model's
// provider, so the decision to run is made on the same sources the request
// authenticates with: the config, the environment, then the store /login wrote.
// Deciding on the environment alone skipped reviews whose key was in one of the
// other two. An empty model is the config's model, which is what --model left
// unset means. The home argument defaults to the user's home directory.
func HasKeyForModel(cfg *config.Config, home string) bool {
	model := ""
	if cfg != nil {
		model = cfg.Model
	}
	if m, ok := provider.GetModel(model); ok && m.Provider == opencodego.ProviderName {
		if _, _, err := providers.ResolveOpenCodeGoKey(cfg, os.Getenv); err == nil {
			return true
		}
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		stored, err := opencode.LoadKey(opencode.DefaultKeyPath(home))
		return err == nil && stored != ""
	}
	return HasProviderKey(home)
}
