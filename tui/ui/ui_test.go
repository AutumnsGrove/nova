//go:build !notui

package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/chat"
	"github.com/AutumnsGrove/nova/tui/client"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// fakeClient records commands and counts Next calls only when a returned
// command is executed, so a test can prove Update does no I/O.
type fakeClient struct {
	sends     []proto.Command
	nexts     int
	responses map[string]proto.Response
	answers   []proto.UIResponse
}

func (f *fakeClient) Send(cmd proto.Command) tea.Cmd {
	return func() tea.Msg {
		f.sends = append(f.sends, cmd)
		if r, ok := f.responses[cmd.Type]; ok {
			return client.ResponseMsg{Response: r}
		}
		return client.ResponseMsg{Response: proto.Response{Type: proto.FrameResponse, Command: cmd.Type, OK: true}}
	}
}

func (f *fakeClient) Next() tea.Cmd { return func() tea.Msg { f.nexts++; return nil } }
func (f *fakeClient) AnswerUI(resp proto.UIResponse) tea.Cmd {
	return func() tea.Msg { f.answers = append(f.answers, resp); return nil }
}

// echoRenderer passes markdown through unchanged.
type echoRenderer struct{}

func (echoRenderer) Markdown(src string, _ int) string { return src }

// panicRenderer panics when asked to render, to exercise View recovery.
type panicRenderer struct{}

func (panicRenderer) Markdown(string, int) string { panic("render boom") }

// fakeClock is a clock whose time a test sets directly.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) After(time.Duration) tea.Cmd {
	return func() tea.Msg { return tickMsg{} }
}

func newModel(t *testing.T) (*Model, *fakeClient, *fakeClock) {
	t.Helper()
	f := &fakeClient{responses: map[string]proto.Response{}}
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	m := New(Options{
		Client: f, Theme: styles.Mono(), Clock: clk, CWD: t.TempDir(),
		Profile: colorprofile.ASCII, Renderer: echoRenderer{}, Width: 80, Height: 24,
	})
	return m, f, clk
}

func update(m *Model, msg tea.Msg) tea.Cmd {
	_, cmd := m.Update(msg)
	return cmd
}

func do(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// execAll runs a command and any commands inside a batch.
func execAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			execAll(c)
		}
	}
}

func runeKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }
func ctrlKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

var enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}
var altEnterKey = tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
var shiftEnterKey = tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}
var escKey = tea.KeyPressMsg{Code: tea.KeyEsc}
var tabKey = tea.KeyPressMsg{Code: tea.KeyTab}
var upKey = tea.KeyPressMsg{Code: tea.KeyUp}
var downKey = tea.KeyPressMsg{Code: tea.KeyDown}
var pgupKey = tea.KeyPressMsg{Code: tea.KeyPgUp}
var pgdownKey = tea.KeyPressMsg{Code: tea.KeyPgDown}

// --- UIM ---

func TestUI_UIM01_InitCommands(t *testing.T) {
	m, f, _ := newModel(t)
	batch, ok := do(m.Init()).(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init did not return a batch: %T", do(m.Init()))
	}
	for _, c := range batch {
		do(c)
	}
	want := []string{proto.CmdGetState, proto.CmdGetCommands, proto.CmdGetMessages}
	for _, w := range want {
		found := false
		for _, s := range f.sends {
			if s.Type == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("Init did not issue %s: %v", w, f.sends)
		}
	}
	if f.nexts != 1 {
		t.Fatalf("Init issued Next %d times, want 1", f.nexts)
	}
}

func TestUI_UIM02_UpdateDoesNoIO(t *testing.T) {
	m, f, _ := newModel(t)
	_ = update(m, runeKey('a'))
	if len(f.sends) != 0 || f.nexts != 0 {
		t.Fatalf("Update called the client synchronously: sends=%d nexts=%d", len(f.sends), f.nexts)
	}
	src, err := os.ReadFile("update.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"os.", "net.", "exec.", "os/", "net/"} {
		if bytes.Contains(src, []byte(bad)) {
			t.Fatalf("update.go contains %q; Update must do no I/O", bad)
		}
	}
}

func TestUI_UIM03_WindowResize(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.width != 100 || m.height != 30 {
		t.Fatalf("size = %dx%d", m.width, m.height)
	}
	if m.layout.Main.W == 0 {
		t.Fatal("layout was not recomputed")
	}
}

func TestUI_UIM04_EventChain(t *testing.T) {
	m, f, _ := newModel(t)
	cmd := update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
	execAll(cmd)
	if f.nexts != 1 {
		t.Fatalf("event did not re-issue Next: %d", f.nexts)
	}
}

func TestUI_UIM05_ClosedQuits(t *testing.T) {
	m, _, _ := newModel(t)
	cmd := update(m, client.ClosedMsg{})
	if m.notice == "" {
		t.Fatal("close did not set a notice")
	}
	if !reflect.DeepEqual(do(cmd), tea.Quit()) {
		t.Fatalf("close returned %T, want quit", do(cmd))
	}
}

func TestUI_UIM06_FooterValues(t *testing.T) {
	m, _, _ := newModel(t)
	m.state.EffectiveLimit = 1000
	m.state.ContextTokens = 420
	m.state.Usage.CostUSD = 0.031
	m.state.Steering = 2
	got := m.footer()
	for _, want := range []string{"42%", "$0.031", "2 queued"} {
		if !strings.Contains(got, want) {
			t.Fatalf("footer %q missing %q", got, want)
		}
	}
}

func TestUI_UIM07_FooterTruncation(t *testing.T) {
	m, _, _ := newModel(t)
	m.width = 40
	m.state.EffectiveLimit = 1000
	m.state.ContextTokens = 420
	m.state.Usage.CostUSD = 0.031
	got := m.footer()
	if strings.Contains(got, "\n") {
		t.Fatalf("footer wrapped: %q", got)
	}
	if w := len([]rune(got)); w > 40 {
		t.Fatalf("footer is %d cells wide: %q", w, got)
	}
}

func TestUI_UIM08_Header(t *testing.T) {
	m, _, _ := newModel(t)
	m.home = "/home/nova"
	m.cwd = "/home/nova/code/proj"
	m.state.Model = "deepseek/x"
	got := m.header()
	for _, want := range []string{"✦", "nova", "deepseek/x", "~/code/proj"} {
		if !strings.Contains(got, want) {
			t.Fatalf("header %q missing %q", got, want)
		}
	}
}

func TestUI_UIM09_RunState(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunStart}})
	if !m.runActive || m.hint() != "esc abort" {
		t.Fatalf("run state not active: active=%v hint=%q", m.runActive, m.hint())
	}
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventRunEnd}})
	if m.runActive {
		t.Fatal("run state did not clear")
	}
}

func TestUI_UIM10_PromptErrorRestores(t *testing.T) {
	m, f, _ := newModel(t)
	m.setEditorText("hi")
	do(update(m, enterKey))
	if len(f.sends) != 1 || f.sends[0].Type != proto.CmdPrompt {
		t.Fatalf("prompt not sent: %v", f.sends)
	}
	update(m, client.ResponseMsg{Response: proto.Response{
		Command: proto.CmdPrompt, OK: false, Error: &proto.Error{Message: "busy"},
	}})
	if m.notice != "busy" {
		t.Fatalf("notice = %q", m.notice)
	}
	if m.editor.Value() != "hi" {
		t.Fatalf("editor not restored: %q", m.editor.Value())
	}
}

func TestUI_UIM11_SessionChangedReloads(t *testing.T) {
	m, f, _ := newModel(t)
	execAll(update(m, client.EventMsg{Event: proto.Event{Type: proto.EventSessionChanged}}))
	found := false
	for _, s := range f.sends {
		if s.Type == proto.CmdGetMessages {
			found = true
		}
	}
	if !found {
		t.Fatalf("session_changed did not request messages: %v", f.sends)
	}
}

func TestUI_UIM12_StateChanged(t *testing.T) {
	m, _, _ := newModel(t)
	raw, _ := json.Marshal(map[string]any{"model": "m2", "effective_limit": 1000})
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventStateChanged, State: raw}})
	if m.state.Model != "m2" || m.state.EffectiveLimit != 1000 {
		t.Fatalf("state not updated: %+v", m.state)
	}
}

func TestUI_UIM13_PanicInViewRecovered(t *testing.T) {
	m, _, _ := newModel(t)
	m.transcript = chat.NewTranscript(80, panicRenderer{}, m.st)
	m.transcript.Apply(proto.Event{Type: proto.EventMessageStart, Run: "r", Turn: intp(1), Role: "assistant"})
	m.transcript.Apply(proto.Event{Type: proto.EventTextDelta, Run: "r", Turn: intp(1), Text: "a\n\nb"})
	m.syncList()
	v := m.View()
	if !strings.Contains(v.Content, "recovered") {
		t.Fatalf("panic not recovered: %q", v.Content)
	}
	if m.panicValue == nil {
		t.Fatal("panic was not recorded for logging")
	}
}

func TestUI_UIM14_ShutdownQuits(t *testing.T) {
	m, f, _ := newModel(t)
	pushUI(m, proto.UIRequest{Type: proto.FrameUIRequest, ID: "a", Method: "select",
		Options: []proto.UIOption{{ID: "x", Label: "X"}}})
	resp := client.ResponseMsg{Response: proto.Response{
		Type: proto.FrameResponse, Command: proto.CmdShutdown, OK: true}}
	msg := do(update(m, resp))
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("shutdown response returned %T, want a batch", msg)
	}
	quit := false
	for _, c := range batch {
		if reflect.DeepEqual(do(c), tea.Quit()) {
			quit = true
		}
	}
	if !quit {
		t.Fatal("shutdown response did not quit")
	}
	if len(f.answers) != 1 || !f.answers[0].Cancelled {
		t.Fatalf("shutdown did not cancel the dialog: %+v", f.answers)
	}
}

func intp(v int) *int { return &v }

// --- EDI ---

func TestUI_EDI01_EnterSubmitsPrompt(t *testing.T) {
	m, f, _ := newModel(t)
	m.setEditorText("hello")
	do(update(m, enterKey))
	if len(f.sends) != 1 || f.sends[0].Type != proto.CmdPrompt || f.sends[0].Text != "hello" {
		t.Fatalf("prompt not sent: %+v", f.sends)
	}
}

func TestUI_EDI02_EmptyIgnored(t *testing.T) {
	m, f, _ := newModel(t)
	m.setEditorText("   ")
	update(m, enterKey)
	if len(f.sends) != 0 {
		t.Fatalf("empty prompt was sent: %v", f.sends)
	}
}

func TestUI_EDI03_SlashCommand(t *testing.T) {
	m, f, _ := newModel(t)
	m.setEditorText("/model x")
	do(update(m, enterKey))
	if len(f.sends) != 1 || f.sends[0].Type != proto.CmdRunCommand || f.sends[0].Name != "model" || f.sends[0].Args != "x" {
		t.Fatalf("run_command not sent: %+v", f.sends)
	}
}

func TestUI_EDI04_BashForms(t *testing.T) {
	m, f, _ := newModel(t)
	m.setEditorText("!ls")
	do(update(m, enterKey))
	if f.sends[0].Type != proto.CmdBash || f.sends[0].Command != "!ls" {
		t.Fatalf("bash not sent: %+v", f.sends[0])
	}
	m.setEditorText("!!ls")
	do(update(m, enterKey))
	if f.sends[1].Type != proto.CmdBash || f.sends[1].Command != "!!ls" {
		t.Fatalf("quiet bash not sent: %+v", f.sends[1])
	}
}

func TestUI_EDI05_EnterWhileRunningSteers(t *testing.T) {
	m, f, _ := newModel(t)
	m.runActive = true
	m.setEditorText("more")
	do(update(m, enterKey))
	if f.sends[0].Type != proto.CmdSteer {
		t.Fatalf("expected steer: %+v", f.sends[0])
	}
}

func TestUI_EDI06_AltEnterWhileRunning(t *testing.T) {
	m, f, _ := newModel(t)
	m.runActive = true
	m.setEditorText("later")
	do(update(m, altEnterKey))
	if f.sends[0].Type != proto.CmdFollowUp {
		t.Fatalf("expected follow_up: %+v", f.sends[0])
	}
}

func TestUI_EDI07_AltEnterWhileIdle(t *testing.T) {
	m, f, _ := newModel(t)
	m.setEditorText("now")
	do(update(m, altEnterKey))
	if f.sends[0].Type != proto.CmdPrompt {
		t.Fatalf("expected prompt: %+v", f.sends[0])
	}
}

func TestUI_EDI08_ShiftEnterNewline(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("a")
	update(m, shiftEnterKey)
	if m.editor.Value() != "a\n" {
		t.Fatalf("editor = %q, want a newline", m.editor.Value())
	}
}

func TestUI_EDI09_UnknownSlashKept(t *testing.T) {
	m, f, _ := newModel(t)
	m.commands = []commandInfo{{Name: "model"}, {Name: "help"}}
	m.setEditorText("/nope")
	update(m, enterKey)
	if !strings.Contains(m.notice, "unknown command") {
		t.Fatalf("notice = %q", m.notice)
	}
	if m.editor.Value() != "/nope" {
		t.Fatalf("text was cleared: %q", m.editor.Value())
	}
	if len(f.sends) != 0 {
		t.Fatalf("unknown command was sent: %v", f.sends)
	}
}

func TestUI_EDI10_EditorHeightGrows(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText(strings.Repeat("line\n", 19) + "line")
	if m.layout.Editor.H != 8 {
		t.Fatalf("editor height = %d, want 8", m.layout.Editor.H)
	}
}

func TestUI_EDI11_LargePaste(t *testing.T) {
	m, f, _ := newModel(t)
	big := strings.Repeat("x", 20000)
	update(m, tea.PasteMsg{Content: big})
	if !strings.Contains(m.editor.Value(), "[pasted 20000 chars]") {
		t.Fatalf("paste not marked: %q", m.editor.Value())
	}
	do(update(m, enterKey))
	if len(f.sends) != 1 || f.sends[0].Text != big {
		t.Fatalf("full paste not sent: len=%d", len(f.sends))
	}
}

func TestUI_EDI12_EditorReturnReplacesText(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("before")
	do(update(m, editorTextMsg{text: "after"}))
	if m.editor.Value() != "after" {
		t.Fatalf("editor = %q, want after", m.editor.Value())
	}
}

func TestUI_EDI13_NoEditorNotice(t *testing.T) {
	m, _, _ := newModel(t)
	t.Setenv("EDITOR", "")
	do(update(m, do(m.openEditor())))
	if !strings.Contains(m.notice, "EDITOR") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestUI_EDI14_QuitCommand(t *testing.T) {
	for _, name := range []string{"quit", "exit"} {
		m, f, _ := newModel(t)
		m.setEditorText("/" + name)
		execAll(update(m, enterKey))
		if !sent(f, proto.CmdShutdown) {
			t.Fatalf("/%s did not send shutdown: %+v", name, f.sends)
		}
		if m.editor.Value() != "" {
			t.Fatalf("/%s left text in the editor: %q", name, m.editor.Value())
		}
	}
}

// --- KEY ---

func TestUI_KEY01_EscAbortsAndRestores(t *testing.T) {
	m, f, _ := newModel(t)
	m.runActive = true
	m.transcript.QueueUser("q", "steer")
	m.syncList()
	do(update(m, escKey))
	if len(f.sends) == 0 || f.sends[0].Type != proto.CmdAbort {
		t.Fatalf("abort not sent: %v", f.sends)
	}
	raw, _ := json.Marshal(map[string]any{"steering": []string{"q"}})
	update(m, client.ResponseMsg{Response: proto.Response{Command: proto.CmdAbort, OK: true, Data: raw}})
	if m.editor.Value() != "q" {
		t.Fatalf("queued message not restored: %q", m.editor.Value())
	}
}

func TestUI_KEY02_EscClosesCompletion(t *testing.T) {
	m, f, _ := newModel(t)
	m.commands = []commandInfo{{Name: "model"}}
	m.openCommands("")
	update(m, escKey)
	if m.completion.active {
		t.Fatal("completion still open")
	}
	if len(f.sends) != 0 {
		t.Fatalf("esc sent a command: %v", f.sends)
	}
}

func TestUI_KEY03_CtrlCClearsEditor(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("hello")
	update(m, ctrlKey('c'))
	if m.editor.Value() != "" {
		t.Fatalf("editor = %q", m.editor.Value())
	}
}

func TestUI_KEY04_CtrlCAbortsRun(t *testing.T) {
	m, f, _ := newModel(t)
	m.runActive = true
	do(update(m, ctrlKey('c')))
	if len(f.sends) == 0 || f.sends[0].Type != proto.CmdAbort {
		t.Fatalf("expected abort: %v", f.sends)
	}
}

func TestUI_KEY05_CtrlCTwiceQuits(t *testing.T) {
	m, f, clk := newModel(t)
	update(m, ctrlKey('c'))
	if !m.quitArmed {
		t.Fatal("quit not armed")
	}
	clk.now = clk.now.Add(time.Second)
	execAll(update(m, ctrlKey('c')))
	if !sent(f, proto.CmdShutdown) {
		t.Fatalf("second ctrl+c did not send shutdown: %+v", f.sends)
	}
	resp := client.ResponseMsg{Response: proto.Response{
		Type: proto.FrameResponse, Command: proto.CmdShutdown, OK: true}}
	if !reflect.DeepEqual(do(update(m, resp)), tea.Quit()) {
		t.Fatal("shutdown response did not quit")
	}
}

func TestUI_KEY06_CtrlCAfterWindow(t *testing.T) {
	m, _, clk := newModel(t)
	update(m, ctrlKey('c'))
	clk.now = clk.now.Add(3 * time.Second)
	if reflect.DeepEqual(do(update(m, ctrlKey('c'))), tea.Quit()) {
		t.Fatal("ctrl+c after 3 s still quit")
	}
	if !m.quitArmed {
		t.Fatal("quit was not re-armed")
	}
}

func TestUI_KEY07_OtherKeyDisarms(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, ctrlKey('c'))
	update(m, runeKey('a'))
	if m.quitArmed {
		t.Fatal("quit still armed")
	}
}

func TestUI_KEY08_PageScroll(t *testing.T) {
	m, _, _ := newModel(t)
	for i := 0; i < 100; i++ {
		m.transcript.AddUser("line", nil)
	}
	m.syncList()
	update(m, pgupKey)
	if m.list.Following() {
		t.Fatal("pgup did not stop following")
	}
	for i := 0; i < 5; i++ {
		update(m, pgdownKey)
	}
	if !m.list.Following() {
		t.Fatal("pgdown did not resume following")
	}
}

func TestUI_KEY09_CtrlOToggles(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, ctrlKey('o'))
	if !m.expanded || !strings.Contains(m.hint(), "collapse") {
		t.Fatalf("expanded=%v hint=%q", m.expanded, m.hint())
	}
	update(m, ctrlKey('o'))
	if m.expanded {
		t.Fatal("ctrl+o did not collapse")
	}
}

func TestUI_KEY10_HistoryRecall(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("one")
	do(update(m, enterKey))
	m.setEditorText("two")
	do(update(m, enterKey))
	update(m, upKey)
	if m.editor.Value() != "two" {
		t.Fatalf("up gave %q, want two", m.editor.Value())
	}
	update(m, upKey)
	if m.editor.Value() != "one" {
		t.Fatalf("second up gave %q, want one", m.editor.Value())
	}
	m.setEditorText("edited")
	if m.history.entries[0] != "one" {
		t.Fatalf("editing changed the stored history: %q", m.history.entries[0])
	}
	update(m, downKey)
	update(m, upKey)
	if m.editor.Value() != "one" {
		t.Fatalf("recall after editing gave %q, want one", m.editor.Value())
	}
}

func TestUI_KEY11_HistoryBounds(t *testing.T) {
	h := &history{}
	for i := 0; i < 600; i++ {
		h.add(strings.Repeat("x", i%5) + string(rune('a'+i%26)))
	}
	if len(h.entries) != historyMax {
		t.Fatalf("history has %d entries, want %d", len(h.entries), historyMax)
	}
	before := len(h.entries)
	h.add(h.entries[len(h.entries)-1])
	if len(h.entries) != before {
		t.Fatal("duplicate of the last entry was not skipped")
	}
}

func TestUI_KEY12_UpInMultiLine(t *testing.T) {
	m, _, _ := newModel(t)
	m.setEditorText("one")
	do(update(m, enterKey))
	m.setEditorText("a\nb")
	for i := 0; i < 10; i++ {
		update(m, downKey)
	}
	update(m, upKey) // cursor on the last line: moves the cursor, not history
	if m.editor.Value() != "a\nb" {
		t.Fatalf("history replaced a multi-line edit: %q", m.editor.Value())
	}
}

func TestUI_KEY14_QuitArmedHint(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, ctrlKey('c'))
	if !m.quitArmed {
		t.Fatal("quit not armed")
	}
	if !strings.Contains(m.footer(), "ctrl+c again to quit") {
		t.Fatalf("footer %q missing the quit hint", m.footer())
	}
}

func TestUI_KEY13_UnavailableKey(t *testing.T) {
	m, _, _ := newModel(t)
	update(m, tea.KeyPressMsg{Code: tea.KeyF12})
	if m.notice != "" || m.editor.Value() != "" {
		t.Fatalf("an unbound key changed state: notice=%q editor=%q", m.notice, m.editor.Value())
	}
}

// --- SCR ---

func TestUI_SCR01_WheelScrolls(t *testing.T) {
	m, _, _ := newModel(t)
	for i := 0; i < 100; i++ {
		m.transcript.AddUser(fmt.Sprintf("line %d", i), nil)
	}
	m.syncList()
	update(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.list.Following() {
		t.Fatal("wheel up did not stop following")
	}
	up := m.list.View()
	update(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.list.View() == up {
		t.Fatal("wheel down did not move the view")
	}
}

func TestUI_SCR02_ScrollUpWhileStreamingStaysPut(t *testing.T) {
	m, _, _ := newModel(t)
	for i := 0; i < 100; i++ {
		m.transcript.AddUser("line", nil)
	}
	m.syncList()
	update(m, pgupKey)
	if m.list.Following() {
		t.Fatal("pgup did not stop following")
	}
	frozen := m.list.View()
	update(m, client.EventMsg{Event: proto.Event{Type: proto.EventError, Error: &proto.Error{Message: "boom"}}})
	if got := m.list.View(); got != frozen {
		t.Fatal("a streaming event moved the view while scrolled up")
	}
}

func TestUI_SCR03_HomeEnd(t *testing.T) {
	m, _, _ := newModel(t)
	for i := 0; i < 100; i++ {
		m.transcript.AddUser("line", nil)
	}
	m.syncList()
	update(m, tea.KeyPressMsg{Code: tea.KeyHome})
	if m.list.Following() {
		t.Fatal("home did not stop following")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if !m.list.Following() {
		t.Fatal("end did not resume following")
	}
}

// --- CPL ---

func TestUI_CPL01_SlashOpensCommands(t *testing.T) {
	m, _, _ := newModel(t)
	m.commands = []commandInfo{{Name: "model"}, {Name: "help"}}
	do(update(m, runeKey('/')))
	if !m.completion.active || m.completion.kind != "command" {
		t.Fatalf("completion not open: %+v", m.completion)
	}
}

func TestUI_CPL02_FilterPrefixThenSubstring(t *testing.T) {
	commands := []commandInfo{{Name: "model"}, {Name: "mcp"}, {Name: "help"}}
	items := filterCommands(commands, "m")
	if items[0].label != "model" || items[1].label != "mcp" {
		t.Fatalf("prefix filter order = %+v", items)
	}
	items = filterCommands(commands, "ode")
	if len(items) != 1 || items[0].label != "model" {
		t.Fatalf("substring filter = %+v", items)
	}
}

func TestUI_CPL03_TabAccepts(t *testing.T) {
	m, _, _ := newModel(t)
	m.commands = []commandInfo{{Name: "model"}}
	m.openCommands("")
	update(m, tabKey)
	if !strings.Contains(m.editor.Value(), "model") {
		t.Fatalf("completion not inserted: %q", m.editor.Value())
	}
}

func TestUI_CPL04_EscKeepsText(t *testing.T) {
	m, _, _ := newModel(t)
	m.commands = []commandInfo{{Name: "model"}}
	do(update(m, runeKey('/')))
	update(m, escKey)
	if m.completion.active {
		t.Fatal("completion still open")
	}
	if m.editor.Value() != "/" {
		t.Fatalf("text = %q, want /", m.editor.Value())
	}
}

func TestUI_CPL05_PathCompletion(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _, _ := newModel(t)
	m.cwd = dir
	msg := do(m.loadPaths("@", 0))
	m.applyCompletion(msg.(completionMsg))
	if !m.completion.active || len(m.completion.items) != 2 {
		t.Fatalf("path completion = %+v", m.completion)
	}
	if m.completion.items[0].label != "sub/" {
		t.Fatalf("directories are not first: %+v", m.completion.items)
	}
}

func TestUI_CPL06_HiddenFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _, _ := newModel(t)
	m.cwd = dir
	msg := do(m.loadPaths("@", 0)).(completionMsg)
	if len(msg.items) != 0 {
		t.Fatalf("hidden file shown without a dot: %+v", msg.items)
	}
	msg = do(m.loadPaths("@.", 0)).(completionMsg)
	if len(msg.items) != 1 || msg.items[0].label != ".secret" {
		t.Fatalf("hidden file not shown after a dot: %+v", msg.items)
	}
}

func TestUI_CPL07_UnreadableDir(t *testing.T) {
	m, _, _ := newModel(t)
	m.cwd = filepath.Join(t.TempDir(), "missing")
	msg := do(m.loadPaths("@x", 0)).(completionMsg)
	if msg.err == nil {
		t.Fatal("expected a read error")
	}
	m.applyCompletion(msg)
	if m.completion.active {
		t.Fatal("completion opened for an unreadable directory")
	}
}

func TestUI_CPL08_SlashMidText(t *testing.T) {
	m, _, _ := newModel(t)
	m.commands = []commandInfo{{Name: "model"}}
	m.setEditorText("hello /")
	m.afterEditorChange()
	if m.completion.active {
		t.Fatal("slash in the middle opened commands")
	}
}

// --- RUN ---

func TestUI_RUN01_RefusesWithoutTerminal(t *testing.T) {
	err := runWith(t.Context(), nil, RunOptions{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != ErrNoTerminal.Error() {
		t.Fatalf("err = %v, want %q", err, ErrNoTerminal)
	}
}

func TestUI_RUN02_NoColor(t *testing.T) {
	if got := pickTheme(RunOptions{NoColor: true}, colorprofile.TrueColor); got.Name != "mono" {
		t.Fatalf("theme = %q, want mono", got.Name)
	}
	m, _, _ := newModel(t)
	if got := m.View().Content; strings.Contains(got, "38;") || strings.Contains(got, "48;") {
		t.Fatalf("mono view has colour escapes: %q", got)
	}
}

func TestUI_RUN03_LogFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f, err := openLog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello log\n")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	data, err := os.ReadFile(filepath.Join(home, ".config", "nova", "tui.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello log") {
		t.Fatalf("log = %q", data)
	}
}

func TestUI_RUN04_ModeSelection(t *testing.T) {
	cases := []struct {
		mode    string
		in, out bool
		want    bool
	}{
		{"tui", false, false, true},
		{"", true, true, true},
		{"", true, false, false},
		{"", false, true, false},
		{"json", true, true, false},
		{"rpc", true, true, false},
	}
	for _, c := range cases {
		if got := Interactive(c.mode, c.in, c.out); got != c.want {
			t.Fatalf("Interactive(%q, %v, %v) = %v, want %v", c.mode, c.in, c.out, got, c.want)
		}
	}
}
