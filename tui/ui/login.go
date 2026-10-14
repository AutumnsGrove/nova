//go:build !notui

package ui

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/dialog"
)

// loginBegunMsg carries a started interactive sign-in back to the model.
type loginBegunMsg struct {
	gen     int
	id      string
	method  providerauth.Method
	session providerauth.Session
	err     error
}

// loginDoneMsg carries the credential an interactive sign-in produced.
type loginDoneMsg struct {
	gen  int
	cred providerauth.Credential
	err  error
}

// credSavedMsg reports the outcome of writing a credential.
type credSavedMsg struct {
	name string
	path string
	err  error
}

// logoutDoneMsg reports the outcome of deleting a credential.
type logoutDoneMsg struct {
	name      string
	remaining providerauth.Status
	err       error
}

// providersLoadedMsg carries the picker rows once their status is resolved.
type providersLoadedMsg struct {
	purpose string // "login" or "logout"
	items   []dialog.ProviderInfo
}

// providerInfosCmd resolves every provider's status off the update loop,
// because Resolve reads the credential store. The config key is empty because
// the TUI reads credentials from the environment and the stores; a hand-edited
// config key is still honoured by the provider.
func (m *Model) providerInfosCmd(purpose string) tea.Cmd {
	home := m.home
	return func() tea.Msg {
		env := os.Getenv
		items := make([]dialog.ProviderInfo, 0, len(providerauth.All()))
		for _, p := range providerauth.All() {
			st := providerauth.Resolve(p, "", env, home)
			items = append(items, dialog.ProviderInfo{
				ID: p.ID, Name: p.Name, Methods: methodLabels(p), Ready: st.Ready, Source: st.Source,
			})
		}
		return providersLoadedMsg{purpose: purpose, items: items}
	}
}

// onProvidersLoaded shows the provider picker.
func (m *Model) onProvidersLoaded(msg providersLoadedMsg) {
	title, intent := "Select provider to configure:", intentLogin
	if msg.purpose == "logout" {
		title, intent = "Select provider to logout:", intentLogout
	}
	m.pushDialog(dialog.NewProviderPicker(title, msg.items), intent)
}

// methodLabels names a provider's methods for the picker.
func methodLabels(p providerauth.Provider) []string {
	out := make([]string, len(p.Methods))
	for i, method := range p.Methods {
		out[i] = method.Label()
	}
	return out
}

// methodByLabel finds a method by the label the picker returned.
func methodByLabel(p providerauth.Provider, label string) (providerauth.Method, bool) {
	for _, method := range p.Methods {
		if method.Label() == label {
			return method, true
		}
	}
	return "", false
}

// parseMethod accepts a method's wire name or its label.
func parseMethod(s string) (providerauth.Method, bool) {
	switch strings.ToLower(strings.ReplaceAll(s, "-", "_")) {
	case "oauth", "subscription":
		return providerauth.OAuth, true
	case "device", "device_code":
		return providerauth.Device, true
	case "api_key", "key":
		return providerauth.APIKey, true
	}
	return "", false
}

// openLoginPicker asks for the provider list for /login.
func (m *Model) openLoginPicker() tea.Cmd { return m.providerInfosCmd("login") }

// openLogoutPicker asks for the provider list for /logout.
func (m *Model) openLogoutPicker() tea.Cmd { return m.providerInfosCmd("logout") }

// loginCommand handles /login [provider [method]].
func (m *Model) loginCommand(args string) tea.Cmd {
	fields := strings.Fields(args)
	switch len(fields) {
	case 0:
		return m.openLoginPicker()
	case 1:
		return m.beginLogin(fields[0])
	default:
		p, ok := providerauth.Get(fields[0])
		if !ok {
			m.notice = "unknown provider: " + fields[0]
			return nil
		}
		method, ok := parseMethod(fields[1])
		if !ok {
			m.notice = "unknown method: " + fields[1]
			return nil
		}
		return m.beginMethod(p, method)
	}
}

// beginLogin starts a provider's sign-in, or opens the method picker when it
// has more than one.
func (m *Model) beginLogin(id string) tea.Cmd {
	p, ok := providerauth.Get(id)
	if !ok {
		m.notice = "unknown provider: " + id
		return nil
	}
	if len(p.Methods) == 1 {
		return m.beginMethod(p, p.Methods[0])
	}
	m.loginProvider = p.ID
	m.pushDialog(dialog.NewMethodPicker(p.Name, methodLabels(p)), intentLoginMethod)
	return nil
}

// beginMethod starts one method. The API key is collected by a dialog; the
// interactive flows call Begin in a command, because Begin does I/O.
func (m *Model) beginMethod(p providerauth.Provider, method providerauth.Method) tea.Cmd {
	m.loginProvider = p.ID
	m.loginMethod = method
	m.loginErr = ""
	if method == providerauth.APIKey {
		m.pushDialog(dialog.NewSecretInput("Paste your "+p.Name+" key", ""), intentLoginKey)
		return nil
	}
	begin, ok := p.Begin[method]
	if !ok {
		m.notice = p.Name + " does not support that sign-in method"
		return nil
	}
	gen := m.loginGen
	cfg := providerauth.Config{Home: m.home, Env: os.Getenv, OpenBrowser: openURL}
	return func() tea.Msg {
		session, err := begin(context.Background(), cfg)
		return loginBegunMsg{gen: gen, id: p.ID, method: method, session: session, err: err}
	}
}

// openURL opens a sign-in page in the platform browser.
func openURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// onLoginBegun shows the started flow and waits for it in the background.
func (m *Model) onLoginBegun(msg loginBegunMsg) tea.Cmd {
	if msg.gen != m.loginGen {
		return nil
	}
	if msg.err != nil {
		m.notice = "sign-in failed: " + msg.err.Error()
		return nil
	}
	m.loginSession = msg.session
	m.pushFlowDialog()
	ctx, cancel := context.WithCancel(context.Background())
	m.loginCancel = cancel
	session, gen := msg.session, msg.gen
	return func() tea.Msg {
		cred, err := session.Wait(ctx)
		return loginDoneMsg{gen: gen, cred: cred, err: err}
	}
}

// pushFlowDialog shows the flow, including any error from a bad paste.
func (m *Model) pushFlowDialog() {
	paste := m.loginMethod == providerauth.OAuth
	d := dialog.NewFlowDialog(m.loginSession.Prompt(), m.loginSession.URL(), m.loginSession.Code(), paste)
	if m.loginErr != "" {
		if f, ok := d.(interface{ Fail(string) }); ok {
			f.Fail(m.loginErr)
		}
	}
	m.pushDialog(d, intentLoginFlow)
}

// onLoginDone saves the credential an interactive flow produced.
func (m *Model) onLoginDone(msg loginDoneMsg) tea.Cmd {
	if msg.gen != m.loginGen {
		return nil
	}
	id, method := m.loginProvider, m.loginMethod
	m.clearLogin()
	if msg.err != nil {
		m.notice = "sign-in failed: " + msg.err.Error()
		return nil
	}
	return m.saveCredential(id, method, msg.cred.Key)
}

// clearLogin forgets the current flow and releases its context.
func (m *Model) clearLogin() {
	if m.loginCancel != nil {
		m.loginCancel()
	}
	m.loginProvider = ""
	m.loginMethod = ""
	m.loginSession = nil
	m.loginCancel = nil
	m.loginErr = ""
}

// cancelLogin stops the current flow and ignores any late result.
func (m *Model) cancelLogin() tea.Cmd {
	if m.loginCancel != nil {
		m.loginCancel()
	}
	if m.loginSession != nil {
		_ = m.loginSession.Close()
	}
	m.loginGen++
	m.clearLogin()
	return nil
}

// saveCredential writes the key in the background.
func (m *Model) saveCredential(id string, method providerauth.Method, key string) tea.Cmd {
	p, ok := providerauth.Get(id)
	if !ok {
		m.notice = "unknown provider: " + id
		return nil
	}
	home := m.home
	return func() tea.Msg {
		path, err := p.Save(home, providerauth.Credential{Method: method, Key: key})
		return credSavedMsg{name: p.Name, path: path, err: err}
	}
}

// onCredSaved reports the save and refreshes the catalog.
func (m *Model) onCredSaved(msg credSavedMsg) tea.Cmd {
	if msg.err != nil {
		m.notice = "could not save the credential: " + msg.err.Error()
		return nil
	}
	m.notice = "logged in to " + msg.name + "; credentials saved to " + msg.path
	return tea.Batch(
		m.client.Send(proto.Command{Type: proto.CmdGetCommands}),
		m.requestModelPicker(),
	)
}

// logoutCommand handles /logout for a provider id.
func (m *Model) logoutCommand(id string) tea.Cmd {
	p, ok := providerauth.Get(id)
	if !ok {
		m.notice = "unknown provider: " + id
		return nil
	}
	home := m.home
	return func() tea.Msg {
		err := p.Delete(home)
		return logoutDoneMsg{name: p.Name, remaining: providerauth.Resolve(p, "", os.Getenv, home), err: err}
	}
}

// onLogoutDone reports the deletion and any source that remains.
func (m *Model) onLogoutDone(msg logoutDoneMsg) {
	if msg.err != nil {
		m.notice = "could not log out: " + msg.err.Error()
		return
	}
	if msg.remaining.Ready {
		m.notice = "logged out of " + msg.name + "; still configured from " + msg.remaining.Source
		return
	}
	m.notice = "logged out of " + msg.name
}
