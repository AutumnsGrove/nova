//go:build !notui

package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tui/git"
	"github.com/AutumnsGrove/nova/tui/layout"
	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/status"
	"github.com/AutumnsGrove/nova/tui/widget"
)

// statusHeight is the number of rows the status region asks the layout for.
func (m *Model) statusHeight() int {
	hasExt := len(m.widgets.StatusSegments()) > 0
	return layout.StatusHeight(m.height, status.Height(m.status, hasExt))
}

// statusView renders the configured status lines plus the extension statuses.
func (m *Model) statusView() []string {
	return status.Build(m.status, m.statusData(), m.width, m.st, extSegments(m.widgets.StatusSegments()))
}

// extSegments converts the store's segments for the renderer.
func extSegments(in []widget.Segment) []status.Ext {
	out := make([]status.Ext, len(in))
	for i, s := range in {
		out[i] = status.Ext{Text: s.Text, Style: s.Style}
	}
	return out
}

// statusData collects the built-in values from the model state.
func (m *Model) statusData() status.Data {
	now := m.clock.Now()
	d := status.Data{
		CWD:           m.shortCWD(),
		Branch:        m.git.Branch,
		GitOK:         m.git.OK,
		Added:         m.git.Added,
		Removed:       m.git.Removed,
		Model:         m.state.Model,
		Thinking:      m.state.Thinking,
		Prompt:        m.state.Usage.PromptTokens,
		Completion:    m.state.Usage.CompletionTokens,
		Cached:        m.state.Usage.CachedTokens,
		Cost:          m.state.Usage.CostUSD,
		ContextTokens: m.state.ContextTokens,
		ContextLimit:  m.state.EffectiveLimit,
		Session:       now.Sub(m.startedAt),
		Queued:        m.queued(),
		Hint:          m.hint(),
		Run:           m.runText(),
	}
	if mod, ok := provider.GetModel(m.state.Model); ok {
		d.Provider = mod.Provider
	}
	if m.runActive && !m.runStartedAt.IsZero() {
		if block := now.Sub(m.runStartedAt); block > 0 {
			d.Block = block
		}
	}
	return d
}

// runText is the run indicator as plain text; the status styles it.
func (m *Model) runText() string {
	if !m.runActive {
		return ""
	}
	elapsed := time.Duration(0)
	if !m.runStartedAt.IsZero() {
		if d := m.clock.Now().Sub(m.runStartedAt); d > 0 {
			elapsed = d
		}
	}
	return render.Spinner(elapsed) + " working " + render.FormatDuration(elapsed)
}

// statusUses reports whether any configured line names one of the segments.
func (m *Model) statusUses(ids ...string) bool {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	for _, line := range status.Lines(m.status) {
		for _, segs := range [][]string{line.Left, line.Right} {
			for _, id := range segs {
				if want[id] {
					return true
				}
			}
		}
	}
	return false
}

// gitRefreshInterval is how often the branch and change counts are refreshed.
const gitRefreshInterval = 3 * time.Second

// gitTickMsg fires a git refresh.
type gitTickMsg struct{}

// gitInfoMsg carries the result of one read back to the model.
type gitInfoMsg struct{ info git.Info }

// scheduleGit arms the next refresh, once, while a git segment is configured.
func (m *Model) scheduleGit() tea.Cmd {
	if m.gitArmed || !m.statusUses("branch", "changes") {
		return nil
	}
	m.gitArmed = true
	after := m.clock.After(gitRefreshInterval)
	return func() tea.Msg {
		after()
		return gitTickMsg{}
	}
}

// readGit runs one read off the update path.
func (m *Model) readGit() tea.Cmd {
	reader, cwd := m.gitReader, m.cwd
	return func() tea.Msg {
		return gitInfoMsg{info: reader.Read(context.Background(), cwd)}
	}
}

// statusClockLive reports whether a configured line shows a live timer.
func (m *Model) statusClockLive() bool { return m.statusUses("session", "block") }
