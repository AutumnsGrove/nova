//go:build !notui

package ui

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
)

// pasteLimit is the character count above which a paste is attached as a
// block instead of inserted into the editor.
const pasteLimit = 8000

// errNoEditor is returned when ctrl+g is pressed without $EDITOR.
var errNoEditor = errors.New("no $EDITOR set")

// setEditorText replaces the editor contents.
func (m *Model) setEditorText(s string) {
	m.editor.SetValue(s)
	m.recompute()
}

// expandPasted replaces paste markers with their full content before sending.
func (m *Model) expandPasted(text string) string {
	for marker, full := range m.pasted {
		text = strings.ReplaceAll(text, marker, full)
	}
	return text
}

// afterEditorChange recomputes completion from the current editor text and
// returns a command when path completion needs to read a directory.
func (m *Model) afterEditorChange() tea.Cmd {
	text := m.editor.Value()
	switch {
	case strings.HasPrefix(text, "/") && !strings.ContainsAny(text, " \n"):
		m.openCommands(strings.TrimPrefix(text, "/"))
		return nil
	default:
		if at := lastAt(text); at >= 0 {
			return m.loadPaths(text, at)
		}
	}
	m.completion.active = false
	return nil
}

// submit turns the editor text into a prompt, steer, follow-up, or command.
// action is "enter" or "follow_up" so alt+enter can differ while a run is
// active.
func (m *Model) submit(action string) tea.Cmd {
	raw := strings.TrimSpace(m.editor.Value())
	if raw == "" {
		return nil
	}
	text := m.expandPasted(raw)
	switch {
	case strings.HasPrefix(raw, "/"):
		name, args := splitCommand(raw)
		if !m.knownCommand(name) && len(m.commands) > 0 {
			m.notice = "unknown command: /" + name
			return nil // leave the text in the editor
		}
		m.setEditorText("")
		m.history.add(raw)
		return m.client.Send(proto.Command{Type: proto.CmdRunCommand, Name: name, Args: args})
	case strings.HasPrefix(raw, "!"):
		m.setEditorText("")
		m.history.add(raw)
		return m.client.Send(proto.Command{Type: proto.CmdBash, Command: text})
	}
	kind := "prompt"
	switch {
	case m.runActive && action == "follow_up":
		kind = "follow_up"
	case m.runActive:
		kind = "steer"
	}
	m.setEditorText("")
	m.history.add(raw)
	m.pending = pendingSend{kind: kind, text: text}
	return m.client.Send(proto.Command{Type: cmdFor(kind), Text: text})
}

// cmdFor maps a send kind to its protocol command.
func cmdFor(kind string) string {
	switch kind {
	case "steer":
		return proto.CmdSteer
	case "follow_up":
		return proto.CmdFollowUp
	default:
		return proto.CmdPrompt
	}
}

// splitCommand splits "/name args" into its name and argument text.
func splitCommand(raw string) (string, string) {
	raw = strings.TrimPrefix(raw, "/")
	if i := strings.IndexAny(raw, " \n"); i >= 0 {
		return raw[:i], strings.TrimSpace(raw[i+1:])
	}
	return raw, ""
}

// knownCommand reports whether a slash command is in the loaded command list.
func (m *Model) knownCommand(name string) bool {
	for _, c := range m.commands {
		if c.Name == name {
			return true
		}
	}
	return false
}

// abort cancels the active run.
func (m *Model) abort() tea.Cmd {
	return m.client.Send(proto.Command{Type: proto.CmdAbort})
}

// editorStartMsg is produced after the temp file for ctrl+g is ready.
type editorStartMsg struct {
	cmd  *exec.Cmd
	path string
	err  error
}

// editorFinishedMsg is delivered when the external editor exits.
type editorFinishedMsg struct {
	path string
	err  error
}

// editorTextMsg carries the text read back from the editor.
type editorTextMsg struct {
	text string
	err  error
}

// openEditor prepares a temp file and the $EDITOR command without touching the
// filesystem in Update.
func (m *Model) openEditor() tea.Cmd {
	text := m.editor.Value()
	return func() tea.Msg {
		ed := os.Getenv("EDITOR")
		if ed == "" {
			return editorStartMsg{err: errNoEditor}
		}
		f, err := os.CreateTemp("", "nova-*.md")
		if err != nil {
			return editorStartMsg{err: err}
		}
		path := f.Name()
		if _, err := f.WriteString(text); err != nil {
			f.Close()
			return editorStartMsg{err: err}
		}
		f.Close()
		return editorStartMsg{cmd: exec.Command(ed, path), path: path}
	}
}

// startEditor returns the ExecProcess command for a prepared editor.
func (m *Model) startEditor(msg editorStartMsg) tea.Cmd {
	if msg.err != nil {
		m.notice = msg.err.Error()
		return nil
	}
	return tea.ExecProcess(msg.cmd, func(err error) tea.Msg {
		return editorFinishedMsg{path: msg.path, err: err}
	})
}

// readEditor reads the temp file back and removes it.
func (m *Model) readEditor(msg editorFinishedMsg) tea.Cmd {
	if msg.err != nil {
		m.notice = msg.err.Error()
		return nil
	}
	path := msg.path
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		os.Remove(path)
		return editorTextMsg{text: string(data), err: err}
	}
}

// applyEditor installs the text returned from the external editor.
func (m *Model) applyEditor(msg editorTextMsg) tea.Cmd {
	if msg.err != nil {
		m.notice = msg.err.Error()
		return nil
	}
	m.setEditorText(strings.TrimRight(msg.text, "\n"))
	return m.afterEditorChange()
}
