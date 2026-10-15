//go:build !notui

package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// quitWindow is how long a second ctrl+c still quits.
const quitWindow = 2 * time.Second

// toggleExpanded flips both expansion flags together, as ctrl+o does.
func (m *Model) toggleExpanded() {
	m.display.ExpandThinking = !m.display.ExpandThinking
	m.display.ExpandTools = !m.display.ExpandTools
	m.expanded = m.display.ExpandTools
	m.transcript.SetDisplay(m.display)
}

// handleKey routes one key press. Special keys are handled here; everything
// else goes to the editor.
func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.Keystroke()
	m.splashOff = true
	m.quitArmed = m.quitArmed && key == "ctrl+c"
	if m.overlay.Len() > 0 {
		r, d := m.overlay.HandleKey(msg)
		if r.Done {
			return m.finishDialog(d, m.popIntent(), r)
		}
		return nil
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
			return m.quit()
		}
		m.quitArmed, m.quitAt = true, m.clock.Now().Add(quitWindow)
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
		m.toggleExpanded()
		return nil
	case "?":
		if strings.TrimSpace(m.editor.Value()) == "" {
			return m.openHelp()
		}
	case "ctrl+l":
		return m.requestModelPicker()
	case "ctrl+r":
		return m.requestSessionList()
	case "ctrl+t":
		return m.cycleThinking()
	case "ctrl+g":
		return m.openEditor()
	}
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	m.lastInput = m.clock.Now()
	m.recompute()
	return tea.Batch(cmd, m.afterEditorChange())
}
