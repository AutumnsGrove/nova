//go:build !notui

// Package ui is the Nova terminal user interface: one Bubble Tea model that
// talks to the server through tui/client and renders with tui/chat. Update
// does no I/O; every side effect is a tea.Cmd.
package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/chat"
	"github.com/AutumnsGrove/nova/tui/client"
	"github.com/AutumnsGrove/nova/tui/dialog"
	"github.com/AutumnsGrove/nova/tui/layout"
	"github.com/AutumnsGrove/nova/tui/list"
	"github.com/AutumnsGrove/nova/tui/styles"
	"github.com/AutumnsGrove/nova/tui/widget"
)

// Clock is the time seam. After returns a command that yields a tick message.
type Clock interface {
	Now() time.Time
	After(d time.Duration) tea.Cmd
}

// systemClock is the wall clock used in production.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
func (systemClock) After(d time.Duration) tea.Cmd {
	return func() tea.Msg { time.Sleep(d); return tickMsg{} }
}

// Options configures a Model. The zero Width and Height are replaced by a
// sensible default so tests can construct a model without a WindowSizeMsg.
type Options struct {
	Client        client.Client
	Theme         styles.Theme
	Clock         Clock
	CWD           string
	Version       string
	Width, Height int
	Profile       colorprofile.Profile
	Renderer      chat.Renderer
}

// commandInfo is one slash command from get_commands.
type commandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"`
}

// stateData is the subset of get_state the UI shows.
type stateData struct {
	Model          string `json:"model"`
	Thinking       string `json:"thinking"`
	ContextLimit   int    `json:"context_limit"`
	Session        string `json:"session"`
	Leaf           string `json:"leaf"`
	Running        bool   `json:"running"`
	ContextTokens  int    `json:"context_tokens"`
	EffectiveLimit int    `json:"effective_limit"`
	CWD            string `json:"cwd"`
	Steering       int    `json:"steering"`
	FollowUp       int    `json:"follow_up"`
	Usage          struct {
		TotalTokens int     `json:"total_tokens"`
		CostUSD     float64 `json:"cost_usd"`
	} `json:"usage"`
}

// Model is the single Bubble Tea model. Sub-structures are plain fields with
// methods; none of them has its own Update.
type Model struct {
	client   client.Client
	clock    Clock
	renderer chat.Renderer
	st       styles.Styles
	theme    styles.Theme
	profile  colorprofile.Profile
	cwd      string
	home     string
	version  string

	width, height int
	editorLines   int

	transcript *chat.Transcript
	list       *list.List
	editor     textarea.Model
	history    history
	pasted     map[string]string
	pasteSeq   int

	commands   []commandInfo
	completion completion
	state      stateData
	pending    pendingSend
	themes     []styles.Theme
	display    chat.Display
	splashOff  bool
	ticking    bool
	usageArmed bool
	runActive  bool
	expanded   bool
	quitArmed  bool
	quitAt     time.Time
	notice     string
	panicValue any

	// The dialog bridge and the extension UI slots.
	overlay    *dialog.Overlay
	widgets    *widget.Store
	queuedReqs []proto.UIRequest
	answered   map[string]bool
	caps       map[string]bool
	intents    []string
	lastInput  time.Time
	title      string
	await      string

	// rendered is the last layout, recomputed on resize.
	layout layout.Regions
}

// New builds a model. It performs no I/O.
func New(opt Options) *Model {
	if opt.Clock == nil {
		opt.Clock = systemClock{}
	}
	if opt.Theme.Name == "" {
		opt.Theme = styles.Dark()
	}
	if opt.Profile == 0 {
		opt.Profile = colorprofile.ASCII
	}
	st := styles.Build(opt.Theme, opt.Profile)
	if opt.Renderer == nil {
		opt.Renderer = chat.NewGlamourRenderer(st)
	}
	if opt.Width == 0 {
		opt.Width = 80
	}
	if opt.Height == 0 {
		opt.Height = 24
	}
	ed := textarea.New()
	ed.ShowLineNumbers = false
	ed.Prompt = "> "
	ed.CharLimit = 0
	ed.Blur()
	if opt.CWD == "" {
		opt.CWD, _ = os.Getwd()
	}
	home, _ := os.UserHomeDir()
	m := &Model{
		client:     opt.Client,
		clock:      opt.Clock,
		renderer:   opt.Renderer,
		st:         st,
		theme:      opt.Theme,
		profile:    opt.Profile,
		cwd:        opt.CWD,
		home:       home,
		version:    opt.Version,
		transcript: chat.NewTranscript(opt.Width, opt.Renderer, st),
		list:       list.New(),
		editor:     ed,
		pasted:     map[string]string{},
		overlay:    dialog.NewOverlay(),
		widgets:    widget.NewStore(),
		answered:   map[string]bool{},
		caps:       capabilitySet(capabilities),
	}
	m.width, m.height = opt.Width, opt.Height
	m.editor.Focus()
	m.themes = styles.LoadThemes(themeDirs(opt.CWD, home))
	m.display = m.transcript.Display()
	m.applyEditorStyles()
	m.recompute()
	return m
}

// applyEditorStyles clears the textarea's hardcoded colours for a mono or
// no-colour profile, and restores them otherwise.
func (m *Model) applyEditorStyles() {
	if m.profile <= colorprofile.ASCII || m.theme.Name == "mono" {
		m.editor.SetStyles(textarea.Styles{})
		return
	}
	m.editor.SetStyles(textarea.DefaultDarkStyles())
}

// themeDirs are the user and project theme directories.
func themeDirs(cwd, home string) []string {
	return []string{
		filepath.Join(config.UserConfigDir(home), "themes"),
		filepath.Join(config.ProjectConfigDir(cwd), "themes"),
	}
}

// setTheme switches the active theme and refreshes every rendered item.
func (m *Model) setTheme(name string) bool {
	for _, th := range m.themes {
		if th.Name == name {
			m.theme = th
			m.st = styles.Build(th, m.profile)
			m.transcript.SetStyles(m.st)
			m.applyEditorStyles()
			return true
		}
	}
	return false
}

// themeList returns the theme names for a notice.
func (m *Model) themeList() string {
	names := make([]string, 0, len(m.themes))
	for _, th := range m.themes {
		names = append(names, th.Name)
	}
	return strings.Join(names, ", ")
}

// showSplash reports whether the splash should replace the transcript.
func (m *Model) showSplash() bool {
	return m.display.Splash && !m.splashOff && len(m.transcript.Items()) == 0
}

// Init issues the first commands: the initial state, commands, messages, and
// the first event wait.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.client.Send(proto.Command{Type: proto.CmdGetState}),
		m.client.Send(proto.Command{Type: proto.CmdGetCommands}),
		m.client.Send(proto.Command{Type: proto.CmdGetMessages}),
		m.client.Send(proto.Command{Type: proto.CmdGetUsage}),
		m.scheduleUsage(),
		m.client.Next(),
	)
}

// Transcript exposes the transcript for tests.
func (m *Model) Transcript() *chat.Transcript { return m.transcript }

// recompute recalculates the layout and resizes the list and editor.
func (m *Model) recompute() {
	lines := m.editor.LineCount()
	aboveH, belowH := 0, 0
	if m.widgets != nil {
		aboveH = m.widgets.Height("above_editor", m.width, widgetMaxLines)
		belowH = m.widgets.Height("below_editor", m.width, widgetMaxLines) +
			m.widgets.Height("sidebar", m.width, widgetMaxLines)
	}
	m.layout = layout.Compute(layout.Inputs{
		Width:       m.width,
		Height:      m.height,
		EditorLines: lines,
		AboveLines:  aboveH,
		BelowLines:  belowH,
	})
	main, editor := m.layout.Main, m.layout.Editor
	m.list.Resize(main.W, main.H)
	m.editor.SetWidth(max(editor.W-3, 1))
	m.editor.SetHeight(max(editor.H, 1))
}

// widgetMaxLines caps one widget placement, matching the widget package.
const widgetMaxLines = 8

// setState decodes a get_state response.
func (m *Model) setState(resp proto.Response) {
	var s stateData
	if err := json.Unmarshal(resp.Data, &s); err != nil {
		return
	}
	m.state = s
	if s.CWD != "" {
		m.cwd = s.CWD
	}
	m.runActive = s.Running
	m.editorLines = m.editor.LineCount()
	m.recompute()
}

// shortCWD replaces the home prefix with ~.
func (m *Model) shortCWD() string {
	cwd := m.cwd
	if m.home != "" && (cwd == m.home || strings.HasPrefix(cwd, m.home+string(os.PathSeparator))) {
		cwd = "~" + strings.TrimPrefix(cwd, m.home)
	}
	return cwd
}

// queued returns the total queued message count.
func (m *Model) queued() int { return m.state.Steering + m.state.FollowUp }

// messages decodes a get_messages response and loads the transcript.
func (m *Model) messages(resp proto.Response) {
	var out struct {
		Messages []message.Message `json:"messages"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return
	}
	m.transcript.Load(out.Messages)
	m.syncList()
}

// syncList pushes the transcript into the list.
func (m *Model) syncList() {
	m.list.SetItems(m.transcript.Items())
}

// usageTickMsg fires when the OpenCode Go usage should refresh.
type usageTickMsg struct{}

// usageRefreshInterval is how often the footer usage is refetched.
const usageRefreshInterval = 5 * time.Minute

// scheduleUsage arms the next usage refresh, once.
func (m *Model) scheduleUsage() tea.Cmd {
	if m.usageArmed {
		return nil
	}
	m.usageArmed = true
	after := m.clock.After(usageRefreshInterval)
	return func() tea.Msg {
		after()
		return usageTickMsg{}
	}
}

// tickMsg is delivered by Clock.After.
type tickMsg struct{}
