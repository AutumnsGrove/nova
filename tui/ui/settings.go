//go:build !notui

package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/tui/dialog"
)

// localCommands are handled inside the TUI, so the server never lists them in
// get_commands. They are still merged into completion and help by allCommands.
var localCommands = []commandInfo{
	{Name: "settings", Description: "edit display settings", Kind: "builtin"},
	{Name: "config", Description: "alias of /settings", Kind: "builtin"},
}

// allCommands is the server's command list plus the TUI's own, for completion
// and help.
func (m *Model) allCommands() []commandInfo {
	out := append([]commandInfo(nil), m.commands...)
	seen := make(map[string]bool, len(out)+len(localCommands))
	for _, c := range out {
		seen[c.Name] = true
	}
	for _, c := range localCommands {
		if !seen[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// noticeMsg surfaces a message produced by an async command.
type noticeMsg struct{ text string }

// openSettings shows the display settings dialog. Its rows are the whole table:
// adding a setting is one row here and one case in applySettings.
func (m *Model) openSettings() tea.Cmd {
	rows := []dialog.Setting{
		{Key: "display.thinking_lines", Label: "Thinking preview lines", Value: m.display.ThinkingPreviewLines, Min: 1, Max: 50},
		{Key: "display.tool_lines", Label: "Tool result lines", Value: m.display.ToolPreviewLines, Min: 1, Max: 50},
		{Key: "display.show_images", Label: "Show images inline (0/1)", Value: boolValue(m.display.ShowImages), Min: 0, Max: 1},
		{Key: "display.image_width_cells", Label: "Image width (cells)", Value: m.display.ImageWidthCells, Min: 20, Max: 200},
	}
	m.pushDialog(dialog.NewSettings("Settings", rows), intentSettings)
	return nil
}

// applySettings applies the dialog's values live and saves each to the user
// config off the update path.
func (m *Model) applySettings(rows []dialog.Setting) tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range rows {
		var persist any = s.Value
		switch s.Key {
		case "display.thinking_lines":
			m.display.ThinkingPreviewLines = s.Value
		case "display.tool_lines":
			m.display.ToolPreviewLines = s.Value
		case "display.show_images":
			m.display.ShowImages = s.Value != 0
			persist = s.Value != 0
		case "display.image_width_cells":
			m.display.ImageWidthCells = s.Value
		default:
			continue
		}
		cmds = append(cmds, m.persistSetting(s.Key, persist))
	}
	m.transcript.SetDisplay(m.display)
	m.expanded = m.display.ExpandTools
	m.syncList()
	return tea.Batch(cmds...)
}

// persistSetting writes one dotted key to the user config. It returns a command
// so the write happens off the update path.
func (m *Model) persistSetting(key string, value any) tea.Cmd {
	home := m.home
	return func() tea.Msg {
		if err := config.SetUser(home, key, value); err != nil {
			return noticeMsg{text: "settings not saved: " + err.Error()}
		}
		return nil
	}
}

// boolValue renders a bool setting as the dialog's 0 or 1.
func boolValue(b bool) int {
	if b {
		return 1
	}
	return 0
}
