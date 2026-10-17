//go:build !notui

package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// quitWindow is how long a second ctrl+c still quits.
const quitWindow = 2 * time.Second

// wheelScroll is how many lines one wheel notch moves the transcript.
const wheelScroll = 3

// editorWheelLines is how many visual rows one wheel notch moves the editor.
const editorWheelLines = 3

// handleMouse routes a mouse event. The wheel scrolls the editor when the
// pointer is over it and its content overflows, and the transcript otherwise;
// a left click toggles the block under the pointer. ItemAt rejects a click
// outside the main region, so only the overlay needs guarding.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		if m.scrollEditor(msg) {
			return nil
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			m.list.ScrollBy(-wheelScroll)
		case tea.MouseWheelDown:
			m.list.ScrollBy(wheelScroll)
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft && m.overlay.Len() == 0 {
			if it := m.list.ItemAt(msg.Y - m.layout.Main.Y); it != nil {
				m.transcript.Toggle(it.ID())
			}
		}
	}
	return nil
}

// scrollEditor moves the editor window when the pointer is over it and its
// content overflows the region. It reports whether it handled the wheel, so
// the transcript keeps the event when the editor has nothing to scroll.
func (m *Model) scrollEditor(msg tea.MouseWheelMsg) bool {
	e := m.layout.Editor
	if e.H <= 0 || msg.Y < e.Y || msg.Y >= e.Y+e.H {
		return false
	}
	full := max(m.editor.Height(), 1)
	maxTop := max(full-e.H, 0)
	if maxTop == 0 {
		return false
	}
	delta := editorWheelLines
	switch msg.Button {
	case tea.MouseWheelUp:
		delta = -delta
	case tea.MouseWheelDown:
	default:
		return false
	}
	m.editorScrollFree = true
	m.editorScroll = min(max(m.editorScroll+delta, 0), maxTop)
	return true
}

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
		m.editorScrollFree = false
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
	case "home":
		m.list.ScrollToTop()
		return nil
	case "end":
		m.list.ScrollToBottom()
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
	m.editorScrollFree = false
	m.editor, cmd = m.editor.Update(msg)
	m.lastInput = m.clock.Now()
	m.recompute()
	return tea.Batch(cmd, m.afterEditorChange())
}
