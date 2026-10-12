//go:build !notui

package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// quitWindow is how long a second ctrl+c still quits.
const quitWindow = 2 * time.Second

// handleKey routes one key press. Special keys are handled here; everything
// else goes to the editor.
func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.Keystroke()
	if m.quitArmed && key != "ctrl+c" {
		m.quitArmed = false
	}
	if m.completion.active {
		switch key {
		case "up":
			m.completion.move(-1)
			return nil
		case "down":
			m.completion.move(1)
			return nil
		case "tab", "enter":
			m.acceptCompletion()
			return m.afterEditorChange()
		case "esc":
			m.completion.active = false
			return nil
		}
	}
	switch key {
	case "esc":
		if m.runActive {
			return m.abort()
		}
		m.completion.active = false
		return nil
	case "ctrl+c":
		if strings.TrimSpace(m.editor.Value()) != "" {
			m.setEditorText("")
			m.completion.active = false
			return nil
		}
		if m.runActive {
			return m.abort()
		}
		if m.quitArmed && m.clock.Now().Before(m.quitAt) {
			return tea.Quit
		}
		m.quitArmed = true
		m.quitAt = m.clock.Now().Add(quitWindow)
		return m.clock.After(quitWindow)
	case "enter":
		return m.submit("enter")
	case "alt+enter":
		return m.submit("follow_up")
	case "shift+enter":
		m.editor.InsertString("\n")
		m.recompute()
		return m.afterEditorChange()
	case "up":
		if m.editor.Line() == 0 {
			if s, ok := m.history.up(); ok {
				m.setEditorText(s)
				return m.afterEditorChange()
			}
		}
	case "down":
		if m.editor.Line() >= m.editor.LineCount()-1 {
			if s, ok := m.history.down(); ok {
				m.setEditorText(s)
				return m.afterEditorChange()
			}
		}
	case "pgup":
		m.list.ScrollBy(-max(m.layout.Main.H, 1))
		return nil
	case "pgdown":
		m.list.ScrollBy(max(m.layout.Main.H, 1))
		return nil
	case "ctrl+o":
		m.expanded = !m.expanded
		return nil
	case "ctrl+l", "ctrl+t", "ctrl+r":
		m.notice = "not available yet"
		return nil
	case "ctrl+g":
		return m.openEditor()
	}
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	m.recompute()
	return tea.Batch(cmd, m.afterEditorChange())
}
