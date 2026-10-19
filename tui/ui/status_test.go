//go:build !notui

package ui

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/git"
	"github.com/AutumnsGrove/nova/tui/status"
)

// GIT01: a read fills the status and the branch segment shows it.
func TestUI_GIT01_GitReadFillsStatus(t *testing.T) {
	m, _, _ := newModel(t)
	m.home, m.cwd = "/home/nova", "/home/nova/proj"
	fake := &git.Fake{Info: git.Info{Branch: "main", Added: 2, Removed: 1, OK: true}}
	m.gitReader = fake
	update(m, m.readGit()())
	if fake.Calls != 1 || !m.git.OK || m.git.Branch != "main" {
		t.Fatalf("git = %+v calls=%d", m.git, fake.Calls)
	}
	text := footerText(m)
	for _, want := range []string{"main", "(+2,-1)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status %q missing %q", text, want)
		}
	}
}

// GIT02: no git refresh is armed when no line uses git.
func TestUI_GIT02_NoGitTickWhenUnused(t *testing.T) {
	m, _, _ := newModel(t)
	m.status = config.StatusConfig{Lines: []config.StatusLine{{Left: []string{"cwd"}}}}
	if m.scheduleGit() != nil {
		t.Fatal("a git refresh was armed with no git segment")
	}
}

// STS05: usage, context, model and thinking render from get_state.
func TestUI_STS05_StatusRendersUsage(t *testing.T) {
	m, _, _ := newModel(t)
	m.home, m.cwd = "/home/nova", "/home/nova/proj"
	m.state.Model = "anthropic/claude-opus-5.5"
	m.state.Thinking = "off"
	m.state.Usage.PromptTokens = 12000
	m.state.Usage.CompletionTokens = 4000
	m.state.Usage.CachedTokens = 9000
	m.state.Usage.CostUSD = 0.02
	m.state.ContextTokens = 42000
	m.state.EffectiveLimit = 100000
	m.git = git.Info{Branch: "main", OK: true}
	text := footerText(m)
	for _, want := range []string{"↑12k", "↓4k", "R9k", "CH75.0%", "$0.020", "42%/100k", "(auto)", "claude-opus-5.5", "thinking off", "main"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status %q missing %q", text, want)
		}
	}
}

// STS06: an extension status grows the region instead of being truncated away.
func TestUI_STS06_ExtensionGrowsTheRegion(t *testing.T) {
	m, _, _ := newModel(t)
	if m.layout.Footer.H != 3 {
		t.Fatalf("status height = %d, want 3", m.layout.Footer.H)
	}
	pushUI(m, proto.UIRequest{Method: "status", Key: "ext",
		Segments: []proto.UISegment{{Text: "go 5h 0.3%"}, {Text: "parallel 92/4,000"}}})
	if m.layout.Footer.H != 5 {
		t.Fatalf("status height = %d, want 3 configured + 2 extension rows", m.layout.Footer.H)
	}
	if !strings.Contains(footerText(m), "parallel") {
		t.Fatalf("extension status missing:\n%s", footerText(m))
	}
}

// STS07: the status height follows the configured line count.
func TestUI_STS07_StatusHeightFromConfig(t *testing.T) {
	m, _, _ := newModel(t)
	m.status = status.Compact()
	m.recompute()
	if m.layout.Footer.H != 1 {
		t.Fatalf("compact status height = %d, want 1", m.layout.Footer.H)
	}
}
