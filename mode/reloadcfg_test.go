package mode

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

// reloadFrom returns a Reload that yields the test server's own base config with
// mutate applied, as if the file on disk had been edited.
func reloadFrom(mutate func(*config.Config)) func() (*config.Config, []string, error) {
	return func() (*config.Config, []string, error) {
		cfg := config.Defaults()
		cfg.Model = "test/model"
		cfg.Tools.Active = []string{"read"}
		cfg.Compaction.Enabled = boolPtr(false)
		cfg.Extensions.Disable = bundledNames()
		mutate(&cfg)
		return &cfg, nil, nil
	}
}

type reloadData struct {
	Reloaded    []string `json:"reloaded"`
	Warnings    []string `json:"warnings"`
	ConfigError string   `json:"config_error"`
}

func doReload(t *testing.T, s *Server) reloadData {
	t.Helper()
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdReload})
	if !resp.OK {
		t.Fatalf("reload = %+v", resp)
	}
	var d reloadData
	decodeData(t, resp, &d)
	return d
}

func stateModel(t *testing.T, s *Server) string {
	t.Helper()
	var st struct {
		Model string `json:"model"`
	}
	decodeData(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdGetState}), &st)
	return st.Model
}

// An edited config takes effect on reload, without a restart.
func TestReload_SRV57_ReloadAppliesTheEditedConfig(t *testing.T) {
	s, _ := testServer(t)
	s.opt.Reload = reloadFrom(func(c *config.Config) {
		c.Display.ThinkingLines = 7
		c.Permissions.Default = permission.Deny
		c.Tools.Active = []string{"read", "glob"}
	})
	d := doReload(t, s)
	if !slices.Contains(d.Reloaded, "config") || d.ConfigError != "" {
		t.Fatalf("reload data = %+v, want config reloaded", d)
	}
	if lines, _, _, _ := s.Display(); lines != 7 {
		t.Errorf("thinking lines = %d, want 7", lines)
	}
	if s.policy.Default != permission.Deny {
		t.Errorf("permissions default = %q, want deny", s.policy.Default)
	}
	if got := strings.Join(slices.Sorted(slices.Values(s.tools.Names())), ","); got != "glob,read" {
		t.Errorf("tools = %q, want glob,read", got)
	}
}

// What the user set at runtime stays on top of the file; what they did not set
// follows the file.
func TestReload_SRV58_RuntimeOverridesSurvive(t *testing.T) {
	s, _ := testServer(t)
	s.opt.Reload = reloadFrom(func(c *config.Config) { c.Model = "test/from-file"; c.Thinking = "high" })
	s.Handle(context.Background(), proto.Command{Type: proto.CmdSetModel, Model: "test/chosen"})
	doReload(t, s)
	if got := stateModel(t, s); got != "test/chosen" {
		t.Errorf("model after reload = %q, want the runtime choice", got)
	}
	if s.cfg.Thinking != "high" {
		t.Errorf("thinking = %q, want the file's value, which was never overridden", s.cfg.Thinking)
	}
	// A second reload still keeps the override.
	s.opt.Reload = reloadFrom(func(c *config.Config) { c.Model = "test/from-file-2" })
	doReload(t, s)
	if got := stateModel(t, s); got != "test/chosen" {
		t.Errorf("model after a second reload = %q, want the runtime choice", got)
	}
}

func TestReload_SRV58b_ANotOverriddenModelFollowsTheFile(t *testing.T) {
	s, _ := testServer(t)
	s.opt.Reload = reloadFrom(func(c *config.Config) { c.Model = "test/from-file" })
	doReload(t, s)
	if got := stateModel(t, s); got != "test/from-file" {
		t.Errorf("model after reload = %q, want the file's", got)
	}
}

// A config that does not load must leave the running one alone, and say so.
func TestReload_SRV59_AFailedLoadChangesNothing(t *testing.T) {
	s, _ := testServer(t)
	s.opt.Reload = func() (*config.Config, []string, error) { return nil, nil, context.DeadlineExceeded }
	d := doReload(t, s)
	if slices.Contains(d.Reloaded, "config") || d.ConfigError == "" {
		t.Fatalf("reload data = %+v, want a config_error and no config", d)
	}
	if !slices.Contains(d.Reloaded, "extensions") {
		t.Errorf("the rest of the reload was skipped: %+v", d)
	}
	if got := stateModel(t, s); got != "test/model" {
		t.Errorf("model = %q after a failed load", got)
	}

	before := strings.Join(s.tools.Names(), ",")
	s.opt.Reload = reloadFrom(func(c *config.Config) { c.Tools.Active = []string{"read", "nope"} })
	d = doReload(t, s)
	if slices.Contains(d.Reloaded, "config") || !strings.Contains(d.ConfigError, "nope") {
		t.Fatalf("reload data = %+v, want the unknown tool reported", d)
	}
	if after := strings.Join(s.tools.Names(), ","); after != before {
		t.Errorf("tools = %q after a rejected config, want %q", after, before)
	}
}

// An embedder with no way to re-read config gets the reload it always had, and
// is not told config was reloaded.
func TestReload_SRV60_WithoutAReloaderConfigIsNotClaimed(t *testing.T) {
	s, _ := testServer(t)
	d := doReload(t, s)
	if slices.Contains(d.Reloaded, "config") {
		t.Fatalf("reloaded = %v, config was not re-read", d.Reloaded)
	}
}

func TestReload_SRV60b_WarningsArePassedOn(t *testing.T) {
	s, _ := testServer(t)
	s.opt.Reload = func() (*config.Config, []string, error) {
		cfg := config.Defaults()
		cfg.Model = "test/model"
		cfg.Tools.Active = []string{"read"}
		cfg.Extensions.Disable = bundledNames()
		return &cfg, []string{"config: unknown key \"colour\""}, nil
	}
	d := doReload(t, s)
	if len(d.Warnings) != 1 || !strings.Contains(d.Warnings[0], "colour") {
		t.Fatalf("warnings = %v", d.Warnings)
	}
}

// The turn budget was fixed when the agent was built; a reload changes it.
func TestReload_SRV61_MaxTurnsFollowsTheFile(t *testing.T) {
	var turns []testkit.Turn
	for range 6 {
		turns = append(turns, testkit.Turn{ToolCalls: []testkit.Call{{ID: "c", Name: "read", Args: `{"path":"missing"}`}}})
	}
	s, _ := testServer(t, turns...)
	s.opt.Reload = reloadFrom(func(c *config.Config) { c.MaxTurns = 2 })
	doReload(t, s)
	events, cancel := s.Subscribe(256)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"})
	var end proto.Event
	for _, ev := range collect(t, events) {
		if ev.Type == proto.EventRunEnd {
			end = ev
		}
	}
	if end.Reason != "exhausted" || end.Turn == nil || *end.Turn != 2 {
		t.Fatalf("run_end = %+v, want exhausted after 2 turns", end)
	}
}

// An embedder that gave its own system prompt keeps it across a reload; the
// built prompt must not replace it.
func TestReload_SRV62_ExplicitSystemPromptSurvives(t *testing.T) {
	s, _ := testServer(t)
	s.opt.SystemPrompt = "you are a test double"
	s.system = s.systemPrompt()
	doReload(t, s)
	if s.system != "you are a test double" {
		t.Fatalf("system prompt = %q after reload", s.system)
	}
}

// /reload is a documented slash command, so the server must run it and say what
// happened in words a person can read.
func TestReload_SRV63_SlashReloadReportsWhatHappened(t *testing.T) {
	s, _ := testServer(t)
	run := func() string {
		resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "reload"})
		if !resp.OK {
			t.Fatalf("/reload = %+v", resp)
		}
		var out struct {
			Text string `json:"text"`
		}
		decodeData(t, resp, &out)
		return out.Text
	}
	if text := run(); strings.Contains(text, "config") {
		t.Errorf("without a reloader the summary mentions config: %q", text)
	}
	s.opt.Reload = reloadFrom(func(c *config.Config) { c.Tools.Active = []string{"nope"} })
	if text := run(); !strings.Contains(text, "Config not reloaded") || !strings.Contains(text, "nope") {
		t.Errorf("a rejected config is not reported: %q", text)
	}
	s.opt.Reload = reloadFrom(func(*config.Config) {})
	if text := run(); !strings.HasPrefix(text, "Reloaded config") {
		t.Errorf("a good reload says %q", text)
	}
	var listed struct {
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
	}
	decodeData(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdGetCommands}), &listed)
	if !slices.ContainsFunc(listed.Commands, func(c struct {
		Name string `json:"name"`
	}) bool {
		return c.Name == "reload"
	}) {
		t.Error("reload is not listed by get_commands")
	}
}
