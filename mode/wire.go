package mode

import (
	"cmp"
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
func buildProvider(cfg *config.Config, home string, sessionID func() string) provider.Provider {
	return providers.Build(providers.Options{
		Config:    cfg,
		Home:      home,
		Getenv:    os.Getenv,
		Store:     auth.NewFileStore(keyPath(home)),
		SessionID: sessionID,
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

// registerTools replaces the registry's contents with the named tools. It
// resolves every name before touching the registry, so a bad list leaves the
// current set exactly as it was instead of half rebuilt.
func registerTools(registry *tool.Registry, pool map[string]tool.Tool, active []string) error {
	chosen := make([]tool.Tool, 0, len(active))
	seen := make(map[string]bool, len(active))
	for _, name := range active {
		t, ok := pool[name]
		if !ok {
			return fmt.Errorf("unknown tool %q", name)
		}
		if seen[name] {
			return fmt.Errorf("tool %q is listed twice", name)
		}
		seen[name] = true
		chosen = append(chosen, t)
	}
	for _, name := range registry.Names() {
		registry.Unregister(name)
	}
	for _, t := range chosen {
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

// The model helpers rely on GetModel returning the zero Model for an id the
// catalog does not know, so each falls back to its zero value.

// modelSlug maps a stable model id to its wire slug when the catalog knows it.
func modelSlug(id string) string {
	m, _ := provider.GetModel(id)
	return cmp.Or(m.Slug, id)
}

// modelWindow is the catalog context window, or 0 when unknown.
func modelWindow(id string) int {
	m, _ := provider.GetModel(id)
	return m.ContextWindow
}

// modelModalities returns a catalog model's declared input kinds, or nil when
// the model is unknown (which the protocol treats as text only).
func modelModalities(id string) []string {
	m, _ := provider.GetModel(id)
	return m.InputModalities
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

// findRoot is the project root for cwd: the nearest ancestor holding .nova or
// .git, or cwd itself when there is none.
func findRoot(cwd string) string {
	if root, ok := config.ProjectRoot(cwd); ok {
		return root
	}
	return cwd
}

// defaultSessionPath returns a new session file for cwd. The name is long
// enough that two sessions started together cannot collide, because opening an
// existing file would silently continue that session.
func defaultSessionPath(cwd string) string {
	return filepath.Join(session.DefaultDir(cwd), randomHex(8)+".jsonl")
}

// randomHex returns n random bytes as lowercase hex. crypto/rand cannot fail
// since Go 1.24 (it ends the process instead), so there is no error to handle.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
