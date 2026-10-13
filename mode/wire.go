package mode

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/AutumnsGrove/nova/auth"
	bundledskills "github.com/AutumnsGrove/nova/bundled/skills"
	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/mode/providers"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/tool"
	"github.com/AutumnsGrove/nova/tools/bash"
	"github.com/AutumnsGrove/nova/tools/fs"
	"github.com/AutumnsGrove/nova/tools/search"
)

// keyPath is where the OpenRouter key is stored under the user config dir.
func keyPath(home string) string {
	return filepath.Join(config.UserConfigDir(home), "auth.json")
}

// trustPath is where the trust store lives.
func trustPath(home string) string {
	return filepath.Join(config.UserConfigDir(home), "trust.json")
}

// buildProvider builds the cross-provider router: OpenRouter plus OpenCode Go.
func buildProvider(cfg *config.Config, home string) provider.Provider {
	return providers.Build(providers.Options{
		Config: cfg,
		Home:   home,
		Getenv: os.Getenv,
		Store:  auth.NewFileStore(keyPath(home)),
	})
}

// defaultToolNames is the built-in set when the config does not choose one.
var defaultToolNames = []string{"read", "write", "edit", "list", "glob", "grep", "bash"}

// buildTools registers the built-in tools, filtered by the config's active list.
// It also returns the full pool so set_tools can replace the active set.
func buildTools(cfg *config.Config) (*tool.Registry, map[string]tool.Tool, error) {
	bashOpts := []bash.Option{}
	if cfg.Tools.Bash.TimeoutSeconds > 0 {
		bashOpts = append(bashOpts, bash.WithTimeout(time.Duration(cfg.Tools.Bash.TimeoutSeconds)*time.Second))
	}
	if cfg.Tools.Bash.MaxOutputBytes > 0 {
		bashOpts = append(bashOpts, bash.WithMaxOutputBytes(cfg.Tools.Bash.MaxOutputBytes))
	}
	all := map[string]tool.Tool{
		"read":  fs.Read(),
		"write": fs.Write(),
		"edit":  fs.Edit(),
		"list":  fs.List(),
		"glob":  search.Glob(),
		"grep":  search.Grep(),
		"bash":  bash.Bash(bashOpts...),
	}
	active := cfg.Tools.Active
	if len(active) == 0 {
		active = defaultToolNames
	}
	registry := tool.NewRegistry()
	if err := registerTools(registry, all, active); err != nil {
		return nil, nil, err
	}
	return registry, all, nil
}

// registerTools replaces the registry's contents with the named tools.
func registerTools(registry *tool.Registry, pool map[string]tool.Tool, active []string) error {
	for _, name := range registry.Names() {
		registry.Unregister(name)
	}
	for _, name := range active {
		t, ok := pool[name]
		if !ok {
			return fmt.Errorf("unknown tool %q", name)
		}
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// registryInfos returns every registered tool's Info in name order.
func registryInfos(registry *tool.Registry) []tool.Info {
	names := registry.Names()
	out := make([]tool.Info, 0, len(names))
	for _, name := range names {
		if info, ok := registry.Info(name); ok {
			out = append(out, info)
		}
	}
	return out
}

// buildSystemPrompt assembles the base prompt, environment, context, skills,
// tools, and the config's extra prompt.
func buildSystemPrompt(cfg *config.Config, cwd, home string, registry *tool.Registry, at time.Time) string {
	trust := config.NewTrustStore(trustPath(home))
	contextFiles, _ := config.LoadContext(cwd, home, config.ContextOptions{Trusted: trust.IsTrusted})
	root := findRoot(cwd)
	skills, _ := config.LoadSkillsWithBuiltins([]string{
		config.SkillsDir(config.ProjectConfigDir(root)),
		config.SkillsDir(config.UserConfigDir(home)),
	}, bundledskills.Builtin())
	return config.BuildSystemPrompt(config.PromptInput{
		Base:    basePrompt,
		Context: contextFiles,
		Skills:  skills,
		Tools:   registryInfos(registry),
		Extra:   cfg.SystemPrompt,
		CWD:     cwd,
		Date:    at,
	})
}

// modelSlug maps a stable model id to its wire slug when the catalog knows it.
func modelSlug(id string) string {
	if m, ok := provider.GetModel(id); ok {
		return m.Slug
	}
	return id
}

// modelWindow is the catalog context window, or 0 when unknown.
func modelWindow(id string) int {
	if m, ok := provider.GetModel(id); ok {
		return m.ContextWindow
	}
	return 0
}

// compactionSettings builds the compactor's settings from the config.
func compactionSettings(cfg *config.Config, model string) compaction.Settings {
	s := compaction.Settings{
		Enabled:      true,
		ContextLimit: cfg.ContextLimit,
		ModelWindow:  modelWindow(model),
		Reserve:      cfg.Compaction.Reserve,
		KeepRecent:   cfg.Compaction.KeepRecent,
	}
	if cfg.Compaction.Enabled != nil {
		s.Enabled = *cfg.Compaction.Enabled
	}
	return s
}

// findRoot walks up for the nearest .nova or .git directory.
func findRoot(cwd string) string {
	dir := cwd
	for {
		if pathExists(filepath.Join(dir, ".nova")) || pathExists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}

// pathExists reports whether a path can be stat'd.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// defaultSessionPath returns a new session file for cwd.
func defaultSessionPath(cwd string) string {
	return filepath.Join(session.DefaultDir(cwd), randomID()+".jsonl")
}

// randomID returns n random bytes as hex.
func randomID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
