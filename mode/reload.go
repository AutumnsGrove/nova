package mode

import (
	"cmp"
	"context"
	"github.com/AutumnsGrove/nova/bundled"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider/openrouter"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/tool"
)

// knobs are the settings a user can change while the server runs. The server
// remembers what the last load read from the file, so a later reload can tell a
// value chosen at runtime from one the file still holds.
type knobs struct {
	model, thinking string
	limit           int
	tools           []string
}

func knobsOf(c *config.Config) knobs {
	return knobs{model: c.Model, thinking: c.Thinking, limit: c.ContextLimit, tools: slices.Clone(c.Tools.Active)}
}

// reloadResult is what a reload did. Reloaded lists what was re-read: config
// only when the embedder gave a way to, and it worked.
type reloadResult struct {
	Reloaded    []string `json:"reloaded"`
	Warnings    []string `json:"warnings,omitempty"`
	ConfigError string   `json:"config_error,omitempty"`
}

// text is the result as one line for a person.
func (r reloadResult) text() string {
	out := "Reloaded " + strings.Join(r.Reloaded, ", ") + "."
	if r.ConfigError != "" {
		out += " Config not reloaded: " + r.ConfigError + "."
	}
	for _, w := range r.Warnings {
		out += " Warning: " + w + "."
	}
	return out
}

// reload waits for the active run, re-reads the config when the embedder gave a
// way to, then rebuilds the system prompt and restarts the extensions. A config
// that cannot be applied is reported in ConfigError and changes nothing; the
// rest of the reload still happens.
func (s *Server) reload(ctx context.Context) (reloadResult, error) {
	for s.agent.Running() {
		if err := s.clock.Sleep(ctx, 10*time.Millisecond); err != nil {
			return reloadResult{}, err
		}
	}
	res := reloadResult{Reloaded: []string{"skills", "templates", "extensions"}}
	if s.opt.Reload != nil {
		if warnings, err := s.reloadConfig(); err != nil {
			res.ConfigError = err.Error()
		} else {
			res.Reloaded = append([]string{"config"}, res.Reloaded...)
			res.Warnings = warnings
		}
	}
	s.mu.Lock()
	s.system = s.systemPrompt()
	s.mu.Unlock()
	for _, err := range s.ext.Reload(ctx) {
		s.emitExtensionError(err)
	}
	s.emitState()
	return res, nil
}

// handleReload is the reload command.
func (s *Server) handleReload(ctx context.Context, cmd proto.Command) proto.Response {
	res, err := s.reload(ctx)
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	return okResponse(cmd, res)
}

// reloadConfig applies a freshly read config to the running server. What the
// user changed at runtime (model, thinking level, context limit, active tools)
// stays on top of the file. The extension host's own settings are not applied: it
// reads them from many goroutines, so they take a restart. Nothing is changed
// unless everything can be.
func (s *Server) reloadConfig() ([]string, error) {
	fresh, warnings, err := s.opt.Reload()
	if err != nil {
		return nil, err
	}
	modelChanged, err := s.applyConfig(fresh)
	if err != nil {
		return nil, err
	}
	diag.SetModel(fresh.Model)
	if modelChanged {
		s.recordEntry(session.Entry{Type: session.TypeModelChange, Model: fresh.Model})
	}
	return warnings, nil
}

// applyConfig swaps fresh in under the lock and reports whether the model
// changed.
func (s *Server) applyConfig(fresh *config.Config) (modelChanged bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file := knobsOf(fresh)
	if s.cfg.Model != s.loaded.model {
		fresh.Model = s.cfg.Model
	}
	if s.cfg.Thinking != s.loaded.thinking {
		fresh.Thinking = s.cfg.Thinking
	}
	if s.cfg.ContextLimit != s.loaded.limit {
		fresh.ContextLimit = s.cfg.ContextLimit
	}
	if !slices.Equal(s.cfg.Tools.Active, s.loaded.tools) {
		fresh.Tools.Active = slices.Clone(s.cfg.Tools.Active)
	}
	fresh.Model = cmp.Or(fresh.Model, openrouter.DefaultModel)

	_, built, err := buildTools(fresh)
	if err != nil {
		return false, err
	}
	pool := maps.Clone(s.pool)
	maps.Copy(pool, built)
	active := s.activeTools(fresh)
	// Resolve every name against a scratch registry first, so a bad list fails
	// before anything has changed.
	if err := registerTools(tool.NewRegistry(), pool, active, bundled.ToolNames(fresh)); err != nil {
		return false, err
	}
	if err := s.agent.Reconfigure(func(c *agent.Config) {
		c.MaxTurns, c.MaxTokens = fresh.MaxTurns, fresh.MaxTokens
		c.Retry = retryPolicy(fresh)
	}); err != nil {
		return false, err
	}
	if err := registerTools(s.tools, pool, active, bundled.ToolNames(fresh)); err != nil {
		return false, err
	}
	s.pool, s.active = pool, active

	modelChanged = fresh.Model != s.cfg.Model
	*s.cfg = *fresh
	if s.opt.Decider == nil {
		s.decider = buildDecider(fresh, s.home)
	}
	if s.opt.Catalog == nil {
		s.catalog = buildDecisionCatalog(fresh, s.home)
	}
	s.loaded = file
	s.model, s.think, s.limit = modelSlug(fresh.Model), fresh.Thinking, fresh.ContextLimit
	s.policy = fresh.Permissions
	s.compactor.Model = s.model
	s.compactor.Settings = compactionSettings(fresh, fresh.Model)
	return modelChanged, nil
}

// activeTools is the tool set for cfg: the options the server was started with
// win over the config, and an empty config list means the defaults.
func (s *Server) activeTools(cfg *config.Config) []string {
	switch {
	case s.opt.NoTools:
		return nil
	case s.opt.Tools != nil:
		return slices.Clone(s.opt.Tools)
	case len(cfg.Tools.Active) > 0:
		return slices.Clone(cfg.Tools.Active)
	}
	return slices.Clone(defaultToolNames)
}

// retryPolicy is the agent's retry settings from the config.
func retryPolicy(cfg *config.Config) agent.RetryPolicy {
	return agent.RetryPolicy{
		Max:    cfg.Retry.Max,
		Base:   time.Duration(cfg.Retry.BaseMS) * time.Millisecond,
		Cap:    time.Duration(cfg.Retry.CapMS) * time.Millisecond,
		Jitter: 0.2,
	}
}

// systemPrompt is the system message: the explicit option when the embedder gave
// one, otherwise the built prompt for the current config, skills and tools.
func (s *Server) systemPrompt() string {
	if s.opt.SystemPrompt != "" {
		return s.opt.SystemPrompt
	}
	return buildSystemPrompt(s.cfg, s.cwd, s.home, s.tools, s.clock.Now())
}
