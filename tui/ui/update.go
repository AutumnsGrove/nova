//go:build !notui

package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/client"
	"github.com/AutumnsGrove/nova/tui/splash"
)

// Update handles one message. It performs no I/O: every side effect is a
// command returned to Bubble Tea.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.recompute()
		return m, nil

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)

	case tea.PasteMsg:
		return m, m.handlePaste(msg.Content)

	case tea.MouseMsg:
		m.handleMouse(msg)
		return m, nil

	case client.ResponseMsg:
		return m, m.handleResponse(msg.Response)

	case client.EventMsg:
		cmd := m.handleEvent(msg.Event)
		return m, tea.Batch(cmd, m.client.Next(), m.scheduleTick())

	case client.UIRequestMsg:
		cmd := m.handleUIRequest(msg.UIRequest)
		return m, tea.Batch(cmd, m.client.Next())

	case client.ClosedMsg:
		m.notice = "connection closed"
		return m, tea.Batch(m.cancelAllDialogs(), tea.Quit)

	case commandsLoadedMsg:
		m.commands = msg.commands
		return m, nil

	case noticeMsg:
		m.notice = msg.text
		return m, nil

	case clipboardMsg:
		m.applyClipboard(msg)
		return m, nil

	case completionMsg:
		m.applyCompletion(msg)
		return m, nil

	case editorStartMsg:
		return m, m.startEditor(msg)

	case editorFinishedMsg:
		return m, m.readEditor(msg)

	case editorTextMsg:
		return m, m.applyEditor(msg)

	case tickMsg:
		m.ticking = false
		before := m.bannerLines()
		if m.splashAnimated() {
			at := m.clock.Now().Sub(m.splashStart)
			m.splashAt = min(at, splash.Sequence)
		}
		if m.bannerLines() != before {
			// The banner stepped down (hero to lockup): recompute so the rows
			// settle. The banner itself stays until the first prompt.
			m.recompute()
		}
		for _, id := range m.transcript.SetNow(m.clock.Now()) {
			m.list.Touch(id)
		}
		m.quitArmed = m.quitArmed && m.clock.Now().Before(m.quitAt)
		return m, m.scheduleTick()

	case gitTickMsg:
		m.gitArmed = false
		return m, tea.Batch(m.readGit(), m.scheduleGit())

	case gitInfoMsg:
		m.git = msg.info
		return m, nil

	case usageTickMsg:
		m.usageArmed = false
		return m, tea.Batch(
			m.client.Send(proto.Command{Type: proto.CmdGetUsage}),
			m.scheduleUsage(),
		)

	case loginBegunMsg:
		return m, m.onLoginBegun(msg)

	case loginDoneMsg:
		return m, m.onLoginDone(msg)

	case credSavedMsg:
		return m, m.onCredSaved(msg)

	case logoutDoneMsg:
		return m, m.onLogoutDone(msg)
	}
	return m, nil
}

// handleResponse routes a command response.
func (m *Model) handleResponse(resp proto.Response) tea.Cmd {
	switch resp.Command {
	case proto.CmdGetState:
		if resp.OK {
			m.setState(resp)
		}
	case proto.CmdGetCommands:
		m.decodeCommands(resp)
	case proto.CmdGetMessages:
		if resp.OK {
			m.messages(resp)
		}
	case proto.CmdListModels:
		if resp.OK {
			m.openModelPicker(resp)
		}
	case proto.CmdSessionList:
		if resp.OK {
			m.openSessionList(resp)
		}
	case proto.CmdSessionTree:
		if resp.OK {
			m.openTree(resp)
		}
	case proto.CmdGetExtensions:
		if resp.OK {
			m.openExtensions(resp)
		}
	case proto.CmdGetUsage:
		m.openUsage(resp)
	case proto.CmdListProviders:
		return m.onProviders(resp)
	case proto.CmdPrompt, proto.CmdSteer, proto.CmdFollowUp:
		m.finishSend(resp)
	case proto.CmdAbort:
		m.finishAbort(resp)
	case proto.CmdBash:
		m.showBashOutput(resp)
	case proto.CmdShutdown:
		// The server has released the session and extensions; leave cleanly.
		return tea.Batch(m.cancelAllDialogs(), tea.Quit)
	case proto.CmdRunCommand:
		if resp.OK {
			var out struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(resp.Data, &out) == nil && out.Text != "" {
				m.notice = out.Text
			}
		} else if resp.Error != nil {
			m.notice = resp.Error.Message
		}
	}
	return nil
}

// decodeCommands parses a get_commands response.
func (m *Model) decodeCommands(resp proto.Response) {
	if !resp.OK {
		return
	}
	var out struct {
		Commands []commandInfo `json:"commands"`
	}
	if json.Unmarshal(resp.Data, &out) == nil {
		m.commands = out.Commands
	}
}

// finishSend handles the response to a prompt, steer, or follow-up.
func (m *Model) finishSend(resp proto.Response) {
	p := m.pending
	m.pending = pendingSend{}
	if !resp.OK {
		if resp.Error != nil {
			m.notice = resp.Error.Message
		}
		m.setEditorText(p.text)
		return
	}
	switch p.kind {
	case "steer":
		m.transcript.QueueUser(p.text, "steer")
	case "follow_up":
		m.transcript.QueueUser(p.text, "follow_up")
	default:
		m.transcript.AddUser(p.text, p.atts)
	}
	m.syncList()
}

// finishAbort puts the returned queued messages back in the editor.
func (m *Model) finishAbort(resp proto.Response) {
	if !resp.OK {
		return
	}
	var out struct {
		Steering []string `json:"steering"`
		FollowUp []string `json:"follow_up"`
	}
	if json.Unmarshal(resp.Data, &out) != nil {
		return
	}
	lines := slices.Concat(out.Steering, out.FollowUp)
	if len(lines) > 0 {
		m.setEditorText(strings.Join(lines, "\n"))
	}
}

// handleEvent folds an event into the transcript and UI state. It returns an
// extra command for events that need a follow-up request. Events do not
// dismiss the splash banner; it plays for its full Sequence regardless.
func (m *Model) handleEvent(e proto.Event) tea.Cmd {
	var cmd tea.Cmd
	switch e.Type {
	case proto.EventRunStart:
		m.setRunning(true)
	case proto.EventRunEnd:
		m.setRunning(false)
	case proto.EventUsage:
		if e.Total != nil {
			m.state.Usage.TotalTokens = e.Total.TotalTokens
			m.state.Usage.CostUSD = e.Total.CostUSD
		} else if e.Usage != nil {
			m.state.Usage.TotalTokens = e.Usage.TotalTokens
			m.state.Usage.CostUSD = e.Usage.CostUSD
		}
	case proto.EventQueueUpdate:
		if e.Steering != nil {
			m.state.Steering = *e.Steering
		}
		if e.FollowUp != nil {
			m.state.FollowUp = *e.FollowUp
		}
	case proto.EventUIExpired:
		m.expireDialog(e.ID)
	case proto.EventStateChanged:
		m.decodeStateEvent(e.State)
	case proto.EventSessionChanged:
		cmd = m.client.Send(proto.Command{Type: proto.CmdGetMessages})
	}
	if changed := m.transcript.Apply(e); len(changed) > 0 {
		m.list.Sync(m.transcript.Items())
	}
	m.recompute()
	return cmd
}

// decodeStateEvent merges a state_changed payload.
func (m *Model) decodeStateEvent(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var s stateData
	if json.Unmarshal(raw, &s) == nil {
		m.state.Model = s.Model
		m.state.Thinking = s.Thinking
		m.state.EffectiveLimit = s.EffectiveLimit
		m.setRunning(s.Running)
	}
}

// handlePaste attaches a large paste as a block instead of inserting it raw.
func (m *Model) handlePaste(content string) tea.Cmd {
	if len(content) > pasteLimit {
		marker := fmt.Sprintf("[pasted %d chars]", len([]rune(content)))
		m.pasted[marker] = content
		m.editor.InsertString(marker)
		return nil
	}
	m.editorScrollFree = false
	m.editor.InsertString(content)
	m.recompute()
	return m.afterEditorChange()
}

// pendingSend remembers an in-flight prompt so an error can restore it.
type pendingSend struct {
	kind string
	text string
	atts []string
}
