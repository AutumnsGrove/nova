//go:build !notui

package ui

import (
	"context"
	"encoding/json"
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

// providerRow is one provider in a list_providers response. The server fills
// it in, so readiness reflects what the server will actually use, including a
// key from its loaded config that the TUI never sees.
type providerRow struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Methods      []string `json:"methods"`
	Ready        bool     `json:"ready"`
	Source       string   `json:"source"`
	Current      bool     `json:"current"`
	DefaultModel string   `json:"default_model"`
}

// decodeProviders reads a list_providers response; a bad body is no rows.
func decodeProviders(resp proto.Response) []providerRow {
	var rows []providerRow
	if resp.OK {
		_ = json.Unmarshal(resp.Data, &rows)
	}
	return rows
}

// requestProviders asks the server for every provider's sign-in status. The
// intent says what to do with the answer.
func (m *Model) requestProviders(intent string) tea.Cmd {
	m.await = intent
	return m.client.Send(proto.Command{Type: proto.CmdListProviders})
}

// onProviders routes a list_providers response by what asked for it.
func (m *Model) onProviders(resp proto.Response) tea.Cmd {
	intent := m.await
	switch intent {
	case intentProvidersAfterLogin:
		m.await = ""
		return m.afterLogin(decodeProviders(resp))
	case intentProvidersLogin, intentProvidersLogout:
		m.await = ""
		title, next := "Select provider to configure:", intentLogin
		if intent == intentProvidersLogout {
			title, next = "Select provider to logout:", intentLogout
		}
		rows := decodeProviders(resp)
		items := make([]dialog.ProviderInfo, 0, len(rows))
		for _, r := range rows {
			labels := make([]string, len(r.Methods))
			for i, method := range r.Methods {
				labels[i] = providerauth.Method(method).Label()
			}
			items = append(items, dialog.ProviderInfo{ID: r.ID, Name: r.Name, Methods: labels, Ready: r.Ready, Source: r.Source})
		}
		m.pushDialog(dialog.NewProviderPicker(title, items), next)
	}
	return nil
}

// afterLogin offers the model picker only when the current model is not served
// by a signed-in provider. A first sign-in lands on a model choice; refreshing
// a key for the provider already in use does not interrupt. When the server
// cannot say which provider is current, the picker opens, the safer error.
func (m *Model) afterLogin(rows []providerRow) tea.Cmd {
	for _, r := range rows {
		if r.Current && r.Ready {
			m.notice += "; /model to choose a model"
			return nil
		}
	}
	return m.requestModelPicker()
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
func (m *Model) openLoginPicker() tea.Cmd { return m.requestProviders(intentProvidersLogin) }

// openLogoutPicker asks for the provider list for /logout.
func (m *Model) openLogoutPicker() tea.Cmd { return m.requestProviders(intentProvidersLogout) }

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
	// Whether to offer a model depends on whether the current one now works,
	// which only the server can say, so ask before deciding.
	return tea.Batch(
		m.client.Send(proto.Command{Type: proto.CmdGetCommands}),
		m.requestProviders(intentProvidersAfterLogin),
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
