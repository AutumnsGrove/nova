//go:build !notui

package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/splash"
)

// pasteLimit is the character count above which a paste is attached as a
// block instead of inserted into the editor.
const pasteLimit = 8000

// errNoEditor is returned when ctrl+g is pressed without $EDITOR.
var errNoEditor = errors.New("no $EDITOR set")

// setEditorText replaces the editor contents and pulls the window back to the
// cursor.
func (m *Model) setEditorText(s string) {
	m.editorScrollFree = false
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
	if raw == "" && len(m.pendingAtts) == 0 {
		return nil
	}
	text := m.expandPasted(raw)
	switch {
	case strings.HasPrefix(raw, "/"):
		name, args := splitCommand(raw)
		if cmd, handled := m.localCommand(name, args); handled {
			m.setEditorText("")
			return cmd
		}
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
		m.dismissSplash()
		return m.client.Send(proto.Command{Type: proto.CmdBash, Command: shellCommand(text)})
	}
	kind := "prompt"
	switch {
	case m.runActive && action == "follow_up":
		kind = "follow_up"
	case m.runActive:
		kind = "steer"
	}
	text = m.attachPaths(text)
	atts := m.attachmentRefs()
	names := m.attachmentNames()
	m.setEditorText("")
	m.history.add(raw)
	m.pendingAtts = nil
	m.pending = pendingSend{kind: kind, text: text, atts: names}
	m.dismissSplash()
	return m.client.Send(proto.Command{Type: cmdFor(kind), Text: text, Attachments: atts})
}

// quietShellPrefix marks a shell command whose output stays out of the model's
// context; the server recognises it, so it is sent through.
const quietShellPrefix = "!!"

// shellCommand turns a `!cmd` line into the bash command the server runs. The
// single ! is only the TUI's marker and must not reach the shell, where it
// would be run as a command name; the quiet !! prefix is the server's flag.
func shellCommand(line string) string {
	if strings.HasPrefix(line, quietShellPrefix) {
		return line
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "!"))
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

// quit asks the server to shut down and quits once it responds. Cancelling the
// dialogs first answers every outstanding UI request, so an extension is not
// left waiting. The shutdown response is what finally quits, so the session
// and extensions are released before the program exits.
func (m *Model) quit() tea.Cmd {
	return tea.Batch(m.cancelAllDialogs(), m.client.Send(proto.Command{Type: proto.CmdShutdown}))
}

// tickInterval is the running-block refresh period.
const tickInterval = 250 * time.Millisecond

// scheduleTick returns a command that fires the next animation tick while a
// thinking block, a tool call, or the splash is live. One tick chain serves
// both, so at most one timer is ever pending.
func (m *Model) scheduleTick() tea.Cmd {
	if m.ticking || !m.needsTick() {
		return nil
	}
	m.ticking = true
	return m.clock.After(m.tickPeriod())
}

// needsTick reports whether anything on screen is animating.
func (m *Model) needsTick() bool {
	return m.runActive || m.transcript.NeedsTick() || m.splashAnimated() || m.loginStatusLive() || m.statusClockLive()
}

// tickPeriod is the shorter of the thinking and splash periods.
func (m *Model) tickPeriod() time.Duration {
	if m.splashAnimated() {
		return splashTick
	}
	if m.runActive || m.transcript.NeedsTick() || m.loginStatusLive() {
		return tickInterval
	}
	return time.Second // only the status clock is live
}

// splashAnimated reports whether an animated splash is showing. It draws a
// throwaway frame, so it is pure: nothing here mutates the model.
func (m *Model) splashAnimated() bool {
	if !m.showSplash() || m.splashAt >= splash.Sequence {
		return false
	}
	_, tier, ok := splash.Render(m.layout.Banner.W, m.layout.Banner.H, m.st, m.profile, m.version, 0)
	return ok && splash.Animated(tier)
}

// localCommand handles TUI-only slash commands before they reach the server.
func (m *Model) localCommand(name, args string) (tea.Cmd, bool) {
	switch name {
	case "model":
		if strings.TrimSpace(args) == "" {
			return m.requestModelPicker(), true
		}
		return nil, false
	case "resume", "sessions":
		return m.requestSessionList(), true
	case "tree":
		return m.requestTree(), true
	case "ext":
		return m.requestExtensions(), true
	case "help":
		m.openHelp()
		return nil, true
	case "usage":
		return m.requestUsage(), true
	case "log", "crash":
		return m.showCrashLog(), true
	case "theme":
		if strings.TrimSpace(args) == "" {
			m.notice = "themes: " + m.themeList()
			return nil, true
		}
		if m.setTheme(strings.TrimSpace(args)) {
			m.notice = "theme: " + strings.TrimSpace(args)
			return nil, true
		}
		m.notice = "unknown theme: " + strings.TrimSpace(args)
		return nil, true
	case "display":
		return m.displayCommand(args), true
	case "settings", "config":
		m.openSettings()
		return nil, true
	case "login":
		return m.loginCommand(args), true
	case "logout":
		if id := strings.TrimSpace(args); id != "" {
			return m.logoutCommand(id), true
		}
		return m.openLogoutPicker(), true
	case "quit", "exit":
		return m.quit(), true
	}
	return nil, false
}

// displayCommand changes one display preference.
func (m *Model) displayCommand(args string) tea.Cmd {
	fields := strings.Fields(args)
	if len(fields) < 2 {
		m.notice = "usage: /display thinking|tools|lines|splash <value>"
		return nil
	}
	key, val := fields[0], fields[1]
	persist := false
	switch key {
	case "thinking":
		if !m.setToggle(&m.display.ExpandThinking, val) {
			return nil
		}
	case "tools":
		if !m.setToggle(&m.display.ExpandTools, val) {
			return nil
		}
	case "lines":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			m.notice = "display lines: expected a positive number"
			return nil
		}
		if n > 50 {
			n = 50
		}
		m.display.ThinkingPreviewLines = n
		persist = true
	case "splash":
		switch val {
		case "on":
			m.display.Splash = true
		case "off":
			m.display.Splash = false
		default:
			m.notice = "display splash: expected on or off"
			return nil
		}
		// The banner is a layout region, so turning it off must reclaim its rows.
		m.recompute()
	default:
		m.notice = "unknown display setting: " + key
		return nil
	}
	m.transcript.SetDisplay(m.display)
	m.expanded = m.display.ExpandTools
	if persist {
		return m.persistSetting("display.thinking_lines", m.display.ThinkingPreviewLines)
	}
	return nil
}

// setToggle applies expand or collapse to a flag.
func (m *Model) setToggle(flag *bool, val string) bool {
	switch val {
	case "expand":
		*flag = true
	case "collapse":
		*flag = false
	default:
		m.notice = "expected expand or collapse, got " + val
		return false
	}
	return true
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
	return func() tea.Msg { return prepareEditor(os.Getenv("EDITOR"), text) }
}

// prepareEditor writes text to a temp file and builds the editor command for
// it. $EDITOR may carry arguments ("code --wait"), so it is split on white
// space before the file name is appended; quoting is not supported.
func prepareEditor(editor, text string) editorStartMsg {
	argv := strings.Fields(editor)
	if len(argv) == 0 {
		return editorStartMsg{err: errNoEditor}
	}
	path, err := writeTempFile(text)
	if err != nil {
		return editorStartMsg{err: err}
	}
	return editorStartMsg{cmd: exec.Command(argv[0], append(argv[1:], path)...), path: path}
}

// writeTempFile stores text in a new private temp file and returns its path.
// A file that could not be written completely is removed, not left behind.
func writeTempFile(text string) (string, error) {
	f, err := os.CreateTemp("", "nova-*.md")
	if err != nil {
		return "", fmt.Errorf("create editor file: %w", err)
	}
	_, werr := f.WriteString(text)
	if err := errors.Join(werr, f.Close()); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write editor file: %w", err)
	}
	return f.Name(), nil
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

// readEditor reads the temp file back and removes it. The file is removed even
// when the editor failed, so an abandoned draft does not pile up in the temp
// directory.
func (m *Model) readEditor(msg editorFinishedMsg) tea.Cmd {
	path := msg.path
	if msg.err != nil {
		m.notice = msg.err.Error()
		return func() tea.Msg {
			_ = os.Remove(path)
			return nil
		}
	}
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		_ = os.Remove(path)
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
