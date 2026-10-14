//go:build !notui

package ui

import (
	"encoding/json"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/dialog"
	"github.com/AutumnsGrove/nova/tui/widget"
)

// Dialog intents decide what a closed local dialog does. Protocol dialogs use
// intentUI and are answered over the wire.
const (
	intentUI         = "ui"
	intentModels     = "models"
	intentSessions   = "sessions"
	intentTree       = "tree"
	intentExtensions = "extensions"
	intentHelp       = "help"
	intentUsage      = "usage"
)

// capable reports whether the client declared a UI method in hello.
func (m *Model) capable(method string) bool {
	return m.caps["ui."+method] || m.caps[method]
}

// handleUIRequest maps a ui_request to a dialog or a fire-and-forget slot.
func (m *Model) handleUIRequest(req proto.UIRequest) tea.Cmd {
	method := strings.TrimPrefix(req.Method, "ui.")
	if !m.capable(method) {
		return nil // an undeclared frame is ignored
	}
	switch method {
	case "notify":
		m.widgets.Notify(req.Text, req.Level, m.clock.Now())
	case "status":
		m.widgets.SetStatus(req.Key, segments(req.Segments))
	case "widget":
		m.applyWidget(req)
	case "title":
		m.title = req.Text
	case "set_editor_text":
		return m.applyEditorText(req.Text)
	case "confirm", "select", "input", "form":
		return m.enqueueOrPush(req, dialogFor(req))
	default:
		return m.answerUnsupported(req)
	}
	return nil
}

// dialogFor builds the dialog for a request method.
func dialogFor(req proto.UIRequest) dialog.Dialog {
	switch strings.TrimPrefix(req.Method, "ui.") {
	case "confirm":
		if req.Remember {
			return dialog.NewPermission(req, false)
		}
		return dialog.NewConfirm(req)
	case "select":
		return dialog.NewSelect(req)
	case "input":
		return dialog.NewInput(req)
	default:
		return dialog.NewForm(req)
	}
}

// enqueueOrPush queues a request while another dialog is open, or shows it.
func (m *Model) enqueueOrPush(req proto.UIRequest, d dialog.Dialog) tea.Cmd {
	if m.overlay.Len() > 0 {
		m.queuedReqs = append(m.queuedReqs, req)
		return nil
	}
	m.pushDialog(d, intentUI)
	return nil
}

// pushDialog adds a dialog and remembers why it was opened.
func (m *Model) pushDialog(d dialog.Dialog, intent string) {
	m.overlay.Push(d)
	m.intents = append(m.intents, intent)
}

// popIntent drops the intent of the dialog that just closed.
func (m *Model) popIntent() string {
	if len(m.intents) == 0 {
		return ""
	}
	intent := m.intents[len(m.intents)-1]
	m.intents = m.intents[:len(m.intents)-1]
	return intent
}

// showNextQueued opens the next queued protocol request.
func (m *Model) showNextQueued() tea.Cmd {
	if m.overlay.Len() > 0 || len(m.queuedReqs) == 0 {
		return nil
	}
	req := m.queuedReqs[0]
	m.queuedReqs = m.queuedReqs[1:]
	m.pushDialog(dialogFor(req), intentUI)
	return nil
}

// finishDialog routes a closed dialog's result.
func (m *Model) finishDialog(d dialog.Dialog, intent string, r dialog.Result) tea.Cmd {
	var cmd tea.Cmd
	if intent == intentUI {
		cmd = m.answerUI(d.ID(), r)
	} else {
		cmd = m.applyLocalIntent(intent, r)
	}
	return tea.Batch(cmd, m.showNextQueued())
}

// answerUI sends one answer per request id.
func (m *Model) answerUI(id string, r dialog.Result) tea.Cmd {
	if id == "" || m.answered[id] {
		return nil
	}
	m.answered[id] = true
	return m.client.AnswerUI(toProtocol(id, r))
}

// answerUnsupported answers a declared but unknown method at once.
func (m *Model) answerUnsupported(req proto.UIRequest) tea.Cmd {
	m.notice = "unsupported UI method: " + req.Method
	if req.ID == "" || m.answered[req.ID] {
		return nil
	}
	m.answered[req.ID] = true
	return m.client.AnswerUI(proto.UIResponse{Type: proto.FrameUIResponse, ID: req.ID, Cancelled: true})
}

// toProtocol converts a dialog result to the wire answer.
func toProtocol(id string, r dialog.Result) proto.UIResponse {
	resp := proto.UIResponse{Type: proto.FrameUIResponse, ID: id, Cancelled: r.Cancelled}
	switch v := r.Value.(type) {
	case proto.UIResponse:
		v.ID = id
		return v
	case []string:
		resp.IDs = v
	case string:
		resp.Text = v
	case []dialog.Answer:
		for _, a := range v {
			resp.Answers = append(resp.Answers, proto.UIAnswer{ID: a.ID, Value: a.Text, Values: a.IDs, Notes: a.Note, Skipped: a.Skipped})
		}
	}
	if !r.Cancelled {
		resp.OK = true
	}
	return resp
}

// applyLocalIntent runs the command behind a local dialog's answer.
func (m *Model) applyLocalIntent(intent string, r dialog.Result) tea.Cmd {
	switch intent {
	case intentModels:
		if id, ok := r.Value.(string); ok && id != "" {
			return m.client.Send(proto.Command{Type: proto.CmdSetModel, Model: id})
		}
	case intentSessions:
		if c, ok := r.Value.(dialog.SessionChoice); ok && c.Path != "" {
			cmd := proto.CmdSessionResume
			if c.Fork {
				cmd = proto.CmdSessionFork
			}
			return m.client.Send(proto.Command{Type: cmd, Session: c.Path})
		}
	case intentTree:
		if id, ok := r.Value.(string); ok && id != "" {
			return m.client.Send(proto.Command{Type: proto.CmdSessionNavigate, Entry: id})
		}
	case intentExtensions:
		if r.Value == "reload" {
			return tea.Batch(
				m.client.Send(proto.Command{Type: proto.CmdReload}),
				m.client.Send(proto.Command{Type: proto.CmdGetExtensions}),
			)
		}
	}
	return nil
}

// cancelAllDialogs answers every open and queued dialog as cancelled.
func (m *Model) cancelAllDialogs() tea.Cmd {
	cancel := dialog.Result{Done: true, Cancelled: true}
	var cmds []tea.Cmd
	for m.overlay.Len() > 0 {
		d := m.overlay.Pop()
		if c := m.answerUI(d.ID(), cancel); c != nil {
			cmds = append(cmds, c)
		}
	}
	for _, req := range m.queuedReqs {
		if c := m.answerUI(req.ID, cancel); c != nil {
			cmds = append(cmds, c)
		}
	}
	m.queuedReqs = nil
	m.intents = nil
	return tea.Batch(cmds...)
}

// expireDialog closes a dialog whose request timed out.
func (m *Model) expireDialog(id string) {
	if top := m.overlay.Top(); top != nil && top.ID() == id {
		m.overlay.Pop()
		m.popIntent()
		m.notice = "the request timed out"
		return
	}
	for i, req := range m.queuedReqs {
		if req.ID == id {
			m.queuedReqs = append(m.queuedReqs[:i], m.queuedReqs[i+1:]...)
			m.notice = "the request timed out"
			return
		}
	}
}

// applyWidget sets or clears a named widget.
func (m *Model) applyWidget(req proto.UIRequest) {
	if len(req.Content) == 0 || string(req.Content) == "null" {
		m.widgets.SetWidget(req.Key, req.Placement, nil)
		return
	}
	var c widget.Content
	if err := json.Unmarshal(req.Content, &c); err != nil {
		m.notice = "bad widget content: " + err.Error()
		return
	}
	m.widgets.SetWidget(req.Key, req.Placement, &c)
}

// applyEditorText replaces the editor text unless the user just typed.
func (m *Model) applyEditorText(text string) tea.Cmd {
	if strings.TrimSpace(m.editor.Value()) != "" && m.clock.Now().Sub(m.lastInput) < editorGrace {
		m.notice = "an extension wants to replace the editor text; clear it to apply"
		return nil
	}
	m.setEditorText(text)
	return nil
}

// editorGrace is how long after typing a set_editor_text is deferred.
const editorGrace = 2 * time.Second

// segments converts protocol segments to widget segments.
func segments(in []proto.UISegment) []widget.Segment {
	out := make([]widget.Segment, 0, len(in))
	for _, s := range in {
		out = append(out, widget.Segment{Text: s.Text, Style: s.Style})
	}
	return out
}

// requestModelPicker asks the server for the catalog.
func (m *Model) requestModelPicker() tea.Cmd {
	m.await = intentModels
	return m.client.Send(proto.Command{Type: proto.CmdListModels})
}

// requestUsage asks the server for the OpenCode Go usage meters.
func (m *Model) requestUsage() tea.Cmd {
	m.await = intentUsage
	return m.client.Send(proto.Command{Type: proto.CmdGetUsage})
}

// openModelPicker shows the catalog from a list_models response.
func (m *Model) openModelPicker(resp proto.Response) {
	if m.await != intentModels {
		return
	}
	m.await = ""
	var rows []struct {
		ID            string  `json:"id"`
		Name          string  `json:"name"`
		Provider      string  `json:"provider"`
		ContextWindow int     `json:"context_window"`
		InPerM        float64 `json:"in_per_m"`
		OutPerM       float64 `json:"out_per_m"`
		Current       bool    `json:"current"`
	}
	if json.Unmarshal(resp.Data, &rows) != nil {
		return
	}
	models := make([]dialog.ModelInfo, 0, len(rows))
	for _, r := range rows {
		models = append(models, dialog.ModelInfo(r))
	}
	m.pushDialog(dialog.NewModelPicker(models, m.state.Model), intentModels)
}

// openUsage sets the footer status from a get_usage response and, when the
// /usage command asked for it, shows the meter overlay.
func (m *Model) openUsage(resp proto.Response) {
	var data struct {
		Rows   []string `json:"rows"`
		Footer string   `json:"footer"`
		Error  string   `json:"error"`
	}
	_ = json.Unmarshal(resp.Data, &data)
	if data.Footer != "" {
		m.widgets.SetStatus("opencode-go", []widget.Segment{{Text: data.Footer, Style: "accent"}})
	} else {
		m.widgets.SetStatus("opencode-go", nil)
	}
	if m.await != intentUsage {
		return
	}
	m.await = ""
	var lines []string
	switch {
	case data.Error != "":
		lines = append(lines, data.Error)
	case len(data.Rows) == 0:
		lines = append(lines, "No usage data yet.")
	default:
		lines = data.Rows
	}
	m.pushDialog(dialog.NewInfo("OpenCode Go — usage", lines), intentUsage)
}

// openSessionList shows the resume picker from a session.list response.
func (m *Model) openSessionList(resp proto.Response) {
	if m.await != intentSessions {
		return
	}
	m.await = ""
	var rows []struct {
		ID       string    `json:"id"`
		Path     string    `json:"path"`
		Name     string    `json:"name"`
		Modified time.Time `json:"modified"`
		Entries  int       `json:"entries"`
		Current  bool      `json:"current"`
	}
	if json.Unmarshal(resp.Data, &rows) != nil {
		return
	}
	items := make([]dialog.SessionInfo, 0, len(rows))
	for _, r := range rows {
		items = append(items, dialog.SessionInfo(r))
	}
	m.pushDialog(dialog.NewSessionList(items), intentSessions)
}

// openTree shows the branch tree from a session.tree response.
func (m *Model) openTree(resp proto.Response) {
	if m.await != intentTree {
		return
	}
	m.await = ""
	var out struct {
		Tree []treeWire `json:"tree"`
	}
	if json.Unmarshal(resp.Data, &out) != nil {
		return
	}
	m.pushDialog(dialog.NewTree(toTree(out.Tree), m.state.Leaf), intentTree)
}

// treeWire is the server's session.tree shape.
type treeWire struct {
	Entry struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Message *struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"entry"`
	Children []treeWire `json:"children"`
}

// toTree converts the wire tree to dialog nodes with previews.
func toTree(nodes []treeWire) []dialog.TreeNode {
	out := make([]dialog.TreeNode, 0, len(nodes))
	for _, n := range nodes {
		preview := n.Entry.Name
		if n.Entry.Message != nil && n.Entry.Message.Content != "" {
			preview = n.Entry.Message.Content
		} else if n.Entry.Content != "" {
			preview = n.Entry.Content
		}
		out = append(out, dialog.TreeNode{
			ID: n.Entry.ID, Type: n.Entry.Type, Preview: preview, Children: toTree(n.Children),
		})
	}
	return out
}

// openExtensions shows the extension status from a get_extensions response.
func (m *Model) openExtensions(resp proto.Response) {
	if m.await != intentExtensions {
		return
	}
	m.await = ""
	var rows []struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		State     string `json:"state"`
		Restarts  int    `json:"restarts"`
		LastError string `json:"last_error"`
	}
	if json.Unmarshal(resp.Data, &rows) != nil {
		return
	}
	items := make([]dialog.ExtInfo, 0, len(rows))
	for _, r := range rows {
		items = append(items, dialog.ExtInfo(r))
	}
	m.pushDialog(dialog.NewExtensions(items), intentExtensions)
}

// requestSessionList asks the server for the session list.
func (m *Model) requestSessionList() tea.Cmd {
	if m.runActive {
		m.notice = "cannot switch sessions while a run is active"
		return nil
	}
	m.await = intentSessions
	return m.client.Send(proto.Command{Type: proto.CmdSessionList})
}

// requestTree asks the server for the session tree.
func (m *Model) requestTree() tea.Cmd {
	if m.runActive {
		m.notice = "cannot switch branches while a run is active"
		return nil
	}
	m.await = intentTree
	return m.client.Send(proto.Command{Type: proto.CmdSessionTree})
}

// requestExtensions asks the server for the extension status.
func (m *Model) requestExtensions() tea.Cmd {
	m.await = intentExtensions
	return m.client.Send(proto.Command{Type: proto.CmdGetExtensions})
}

// openHelp shows the key and command reference.
func (m *Model) openHelp() tea.Cmd {
	keys := []dialog.KeyHelp{
		{Key: "enter", Description: "send prompt"},
		{Key: "alt+enter", Description: "queue a follow-up"},
		{Key: "esc", Description: "abort the run / close a dialog"},
		{Key: "ctrl+o", Description: "expand thinking and tools"},
		{Key: "ctrl+g", Description: "edit in $EDITOR"},
		{Key: "ctrl+l", Description: "pick a model"},
		{Key: "ctrl+r", Description: "resume a session"},
		{Key: "ctrl+t", Description: "cycle thinking effort"},
		{Key: "ctrl+c twice", Description: "quit"},
	}
	var cmds []string
	for _, c := range m.commands {
		cmds = append(cmds, c.Name)
	}
	m.pushDialog(dialog.NewHelp(keys, cmds), intentHelp)
	return nil
}

// cycleThinking advances off -> low -> medium -> high -> off.
func (m *Model) cycleThinking() tea.Cmd {
	order := []string{"off", "low", "medium", "high"}
	cur := m.state.Thinking
	if cur == "" {
		cur = "off"
	}
	next := order[0]
	for i, lvl := range order {
		if lvl == cur {
			next = order[(i+1)%len(order)]
			break
		}
	}
	m.state.Thinking = next
	m.notice = "thinking: " + next
	return m.client.Send(proto.Command{Type: proto.CmdSetThinking, Level: next})
}
