// Package firstrun is the flow a new user sees before the chat screen: log in
// or paste a key, then pick a model. It is a Bubble Tea model; cmd/nova runs it
// before building the server so the saved key and model are picked up.
package firstrun

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider"
)

// Step is one screen in the first-run flow.
type Step int

const (
	// Login offers OpenRouter login or a pasted key.
	Login Step = iota
	// Model picks a model from the catalog.
	Model
	// Done ends the flow.
	Done
)

// Needed returns the steps to run. A resolvable key means no flow; a missing
// key runs Login then Model, so the flow runs again only while the key is
// missing (FRS08, FRS12).
func Needed(hasKey bool, cfg *config.Config) []Step {
	if hasKey {
		return nil
	}
	return []Step{Login, Model}
}

// Options configures the flow. The callbacks keep network and file I/O out of
// Update, so the model is testable without a terminal or a real store.
type Options struct {
	Catalog       []provider.Model
	DefaultModel  string
	Version       string
	Width, Height int
	In            io.Reader
	Out           io.Writer
	Login         func(context.Context) (string, error)
	SaveKey       func(string) error
	SaveModel     func(string) error
}

// Result is what the flow changed, if anything.
type Result struct {
	KeySaved   bool
	ModelSaved bool
	Skipped    bool
}

// loginDoneMsg carries the outcome of the OpenRouter login command.
type loginDoneMsg struct {
	key string
	err error
}

// Model is the first-run Bubble Tea model.
type Flow struct {
	opt     Options
	step    Step
	width   int
	height  int
	choice  int // 0 log in, 1 paste a key
	pasting bool
	input   []rune
	errText string
	busy    bool
	cursor  int

	keySaved   bool
	modelSaved bool
	skipped    bool
	done       bool
}

// New builds the model with the catalog's default model highlighted.
func New(opt Options) Flow {
	m := Flow{opt: opt, step: Login, width: opt.Width, height: opt.Height}
	if m.width == 0 {
		m.width = 80
	}
	if m.height == 0 {
		m.height = 24
	}
	for i, model := range opt.Catalog {
		if model.ID == opt.DefaultModel {
			m.cursor = i
			break
		}
	}
	return m
}

// Init implements tea.Model.
func (m Flow) Init() tea.Cmd { return nil }

// Result reports what the flow changed.
func (m Flow) Result() Result {
	return Result{KeySaved: m.keySaved, ModelSaved: m.modelSaved, Skipped: m.skipped}
}

// Step reports the current screen.
func (m Flow) Step() Step { return m.step }

// Update implements tea.Model.
func (m Flow) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case loginDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.errText = msg.err.Error()
			return m, nil
		}
		if err := m.saveKey(msg.key); err != nil {
			m.errText = err.Error()
			return m, nil
		}
		m.keySaved = true
		m.step = Model
		m.errText = ""
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg.Keystroke())
	}
	return m, nil
}

// handleKey routes one key press for the current screen.
func (m Flow) handleKey(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		m.done = true
		return m, tea.Quit
	}
	switch m.step {
	case Login:
		return m.handleLoginKey(key)
	case Model:
		return m.handleModelKey(key)
	}
	return m, nil
}

func (m Flow) handleLoginKey(key string) (tea.Model, tea.Cmd) {
	if key == "esc" {
		m.skipped = true
		m.done = true
		return m, tea.Quit
	}
	if m.pasting {
		switch key {
		case "enter":
			value := strings.TrimSpace(string(m.input))
			if value == "" {
				m.errText = "the key is empty"
				return m, nil
			}
			if err := m.saveKey(value); err != nil {
				m.errText = err.Error()
				return m, nil
			}
			m.keySaved = true
			m.step = Model
			m.pasting = false
			m.input = nil
			m.errText = ""
		case "backspace":
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		default:
			if len([]rune(key)) == 1 {
				m.input = append(m.input, []rune(key)[0])
			}
		}
		return m, nil
	}
	switch key {
	case "up", "down", "tab":
		m.choice = 1 - m.choice
	case "enter":
		if m.busy {
			return m, nil
		}
		if m.choice == 1 {
			m.pasting = true
			m.errText = ""
			return m, nil
		}
		m.busy = true
		m.errText = ""
		login := m.opt.Login
		return m, func() tea.Msg {
			if login == nil {
				return loginDoneMsg{err: fmt.Errorf("login is not available")}
			}
			key, err := login(context.Background())
			return loginDoneMsg{key: key, err: err}
		}
	}
	return m, nil
}

func (m Flow) handleModelKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.skipped = true
		m.done = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.opt.Catalog)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.opt.Catalog) == 0 {
			m.errText = "no models available"
			return m, nil
		}
		id := m.opt.Catalog[m.cursor].ID
		if err := m.saveModel(id); err != nil {
			m.errText = err.Error()
			return m, nil
		}
		m.modelSaved = true
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

// saveKey calls SaveKey when one is set.
func (m Flow) saveKey(key string) error {
	if m.opt.SaveKey == nil {
		return nil
	}
	return m.opt.SaveKey(key)
}

// saveModel calls SaveModel when one is set.
func (m Flow) saveModel(id string) error {
	if m.opt.SaveModel == nil {
		return nil
	}
	return m.opt.SaveModel(id)
}

// View implements tea.Model. The pasted key is rendered as bullets and never
// appears, on screen or in the model's text.
func (m Flow) View() tea.View {
	var b strings.Builder
	b.WriteString("Welcome to Nova\n\n")
	switch m.step {
	case Login:
		if m.pasting {
			fmt.Fprintf(&b, "Paste your OpenRouter key:\n\n  %s\n\n", strings.Repeat("•", len(m.input)))
		} else {
			b.WriteString("Log in to get started.\n\n")
			options := []string{"Log in with OpenRouter", "Paste a key"}
			for i, option := range options {
				marker := "  "
				if i == m.choice {
					marker = "> "
				}
				fmt.Fprintf(&b, "%s%s\n", marker, option)
			}
			b.WriteString("\nenter select  esc skip\n")
		}
	case Model:
		b.WriteString("Pick a model:\n\n")
		for i, model := range m.opt.Catalog {
			marker := "  "
			if i == m.cursor {
				marker = "> "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, model.ID)
		}
		b.WriteString("\nenter select  esc skip\n")
	}
	if m.busy {
		b.WriteString("\nOpening your browser…\n")
	}
	if m.errText != "" {
		fmt.Fprintf(&b, "\nerror: %s\nenter to retry, esc to skip\n", m.errText)
	}
	return tea.NewView(b.String())
}

// Run drives the flow on a terminal. A nil error with a skipped result means
// the app should start and report the missing key on the first prompt.
func Run(ctx context.Context, opt Options) (Result, error) {
	if opt.In == nil || opt.Out == nil {
		return Result{Skipped: true}, nil
	}
	final, err := tea.NewProgram(New(opt), tea.WithContext(ctx), tea.WithInput(opt.In), tea.WithOutput(opt.Out)).Run()
	if err != nil {
		return Result{}, err
	}
	if model, ok := final.(Flow); ok {
		return model.Result(), nil
	}
	return Result{}, nil
}
