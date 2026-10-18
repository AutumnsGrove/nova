// Package firstrun is the flow a new user sees before the chat screen: sign in
// or paste a key, then pick a model. It is a Bubble Tea model; cmd/nova runs it
// before building the server so the saved key and model are picked up.
package firstrun

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/provider"
)

// Step is one screen in the first-run flow.
type Step int

const (
	// Login offers every registered provider's sign-in methods.
	Login Step = iota
	// Model picks a model from the catalog.
	Model
	// Done ends the flow.
	Done
)

// SignIn is the login backend. providerauth.Login is the real one; a test
// supplies a fake, so the flow never touches the network or a credential store.
type SignIn interface {
	Providers() []providerauth.Provider
	Begin(context.Context, providerauth.Provider, providerauth.Method) (providerauth.Session, error)
	Save(providerauth.Provider, providerauth.Credential) error
}

// loginMode is the login step's sub-screen.
type loginMode int

const (
	listMode loginMode = iota
	keyMode
	waitMode
)

// loginChoice is one row of the login list: a provider's method.
type loginChoice struct {
	provider providerauth.Provider
	method   providerauth.Method
}

// label is the row as the screen shows it.
func (c loginChoice) label() string {
	if c.method == providerauth.APIKey {
		return "Paste your " + c.provider.Name + " key"
	}
	return "Sign in with " + c.provider.Name
}

// Needed returns the steps to run. A resolvable key means no flow; a missing
// key runs Login then Model, so the flow runs again only while the key is
// missing (FRS08, FRS12).
func Needed(hasKey bool, cfg *config.Config) []Step {
	if hasKey {
		return nil
	}
	return []Step{Login, Model}
}

// Options configures the flow. The SignIn seam keeps network and file I/O out
// of Update, so the model is testable without a terminal or a real store.
type Options struct {
	Catalog      []provider.Model
	DefaultModel string
	In           io.Reader
	Out          io.Writer
	SignIn       SignIn
	SaveModel    func(string) error
}

// Result is what the flow changed, if anything.
type Result struct {
	KeySaved   bool
	ModelSaved bool
	Skipped    bool
}

// beginDoneMsg carries a started sign-in back to the model.
type beginDoneMsg struct {
	session providerauth.Session
	err     error
}

// loginDoneMsg carries the credential a sign-in produced.
type loginDoneMsg struct {
	cred providerauth.Credential
	err  error
}

// credSavedMsg reports the outcome of storing a credential.
type credSavedMsg struct {
	err error
}

// Model is the first-run Bubble Tea model.
type Flow struct {
	opt  Options
	step Step

	choices []loginChoice
	choice  int
	mode    loginMode
	pending loginChoice
	session providerauth.Session
	input   []rune
	errText string
	busy    bool

	cursor     int
	keySaved   bool
	modelSaved bool
	skipped    bool
	done       bool
}

// New builds the model with the catalog's default model highlighted and the
// login list built from the sign-in registry.
func New(opt Options) Flow {
	m := Flow{opt: opt, step: Login}
	if m.opt.SignIn == nil {
		m.opt.SignIn = providerauth.Login{}
	}
	for _, p := range m.opt.SignIn.Providers() {
		for _, method := range p.Methods {
			m.choices = append(m.choices, loginChoice{provider: p, method: method})
		}
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
	case beginDoneMsg:
		return m.onBegin(msg)
	case loginDoneMsg:
		return m.onLoginDone(msg)
	case credSavedMsg:
		return m.onCredSaved(msg)
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

// handleLoginKey routes a key on the login step. Esc always skips the flow and
// releases a running sign-in.
func (m Flow) handleLoginKey(key string) (tea.Model, tea.Cmd) {
	if key == "esc" {
		if m.session != nil {
			_ = m.session.Close()
			m.session = nil
		}
		m.skipped = true
		m.done = true
		return m, tea.Quit
	}
	switch m.mode {
	case keyMode:
		return m.handleKeyInput(key)
	case waitMode:
		if key == "enter" && m.errText != "" {
			return m.startChoice(m.pending)
		}
		return m, nil
	}
	if m.busy {
		return m, nil
	}
	switch key {
	case "up", "k":
		if m.choice > 0 {
			m.choice--
		}
	case "down", "j":
		if m.choice < len(m.choices)-1 {
			m.choice++
		}
	case "enter":
		if len(m.choices) == 0 {
			m.errText = "no sign-in method is available"
			return m, nil
		}
		return m.startChoice(m.choices[m.choice])
	}
	return m, nil
}

// handleKeyInput collects a pasted API key. The key is never rendered.
func (m Flow) handleKeyInput(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		value := strings.TrimSpace(string(m.input))
		if value == "" {
			m.errText = "the key is empty"
			return m, nil
		}
		return m.save(m.pending, providerauth.Credential{Method: providerauth.APIKey, Key: value})
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	default:
		if r := []rune(key); len(r) == 1 && r[0] >= ' ' {
			m.input = append(m.input, r[0])
		}
	}
	return m, nil
}

// startChoice begins one method: an API key is collected here, anything else
// starts the provider's flow.
func (m Flow) startChoice(c loginChoice) (tea.Model, tea.Cmd) {
	m.pending = c
	m.errText = ""
	if c.method == providerauth.APIKey {
		m.mode = keyMode
		m.input = nil
		return m, nil
	}
	m.busy = true
	signIn := m.opt.SignIn
	return m, func() tea.Msg {
		session, err := signIn.Begin(context.Background(), c.provider, c.method)
		return beginDoneMsg{session: session, err: err}
	}
}

// onBegin shows the started flow and waits for it in the background.
func (m Flow) onBegin(msg beginDoneMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	if msg.err != nil {
		m.mode = listMode
		m.errText = "sign-in failed: " + msg.err.Error()
		return m, nil
	}
	m.session = msg.session
	m.mode = waitMode
	m.errText = ""
	session := msg.session
	return m, func() tea.Msg {
		cred, err := session.Wait(context.Background())
		return loginDoneMsg{cred: cred, err: err}
	}
}

// onLoginDone stores the credential a flow produced.
func (m Flow) onLoginDone(msg loginDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.mode = waitMode
		m.errText = msg.err.Error()
		return m, nil
	}
	m.session = nil
	return m.save(m.pending, msg.cred)
}

// save writes the credential in the background, keeping I/O out of Update.
func (m Flow) save(c loginChoice, cred providerauth.Credential) (tea.Model, tea.Cmd) {
	m.busy = true
	m.errText = ""
	signIn := m.opt.SignIn
	return m, func() tea.Msg {
		return credSavedMsg{err: signIn.Save(c.provider, cred)}
	}
}

// onCredSaved advances to the model step once the key is stored.
func (m Flow) onCredSaved(msg credSavedMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	if msg.err != nil {
		m.mode = listMode
		m.errText = "could not save the key: " + msg.err.Error()
		return m, nil
	}
	m.keySaved = true
	m.mode = listMode
	m.pending = loginChoice{}
	m.input = nil
	m.errText = ""
	m.step = Model
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

// saveModel calls SaveModel when one is set.
func (m Flow) saveModel(id string) error {
	if m.opt.SaveModel == nil {
		return nil
	}
	return m.opt.SaveModel(id)
}

// View implements tea.Model. A pasted key is rendered as bullets and never
// appears, on screen or in the model's text.
func (m Flow) View() tea.View {
	var b strings.Builder
	b.WriteString("Welcome to Nova\n\n")
	switch m.step {
	case Login:
		m.loginView(&b)
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

// loginView renders the login step's current sub-screen.
func (m Flow) loginView(b *strings.Builder) {
	switch m.mode {
	case keyMode:
		fmt.Fprintf(b, "Paste your %s key:\n\n  %s\n", m.pending.provider.Name, strings.Repeat("•", len(m.input)))
	case waitMode:
		if m.session == nil {
			b.WriteString("Waiting for sign-in…\n")
			return
		}
		fmt.Fprintf(b, "%s\n\n", m.session.Prompt())
		if url := m.session.URL(); url != "" {
			b.WriteString("Open this page in your browser:\n  " + url + "\n\n")
		}
		if code := m.session.Code(); code != "" {
			b.WriteString("code: " + code + "\n\n")
		}
		if r, ok := m.session.(providerauth.StatusReporter); ok {
			if s := r.Status(); s != "" {
				b.WriteString(s + "\n")
			}
		}
		b.WriteString("Waiting for sign-in…  esc skip\n")
	default:
		if len(m.choices) == 0 {
			b.WriteString("No sign-in method is available.\n")
			return
		}
		b.WriteString("Sign in to get started.\n\n")
		for i, c := range m.choices {
			marker := "  "
			if i == m.choice {
				marker = "> "
			}
			fmt.Fprintf(b, "%s%s\n", marker, c.label())
		}
		b.WriteString("\nenter select  esc skip\n")
	}
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
