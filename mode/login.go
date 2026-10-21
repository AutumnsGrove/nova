package mode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/proto"
)

// loginFlow is one interactive sign-in in progress. The server holds at most
// one per provider, so the provider id is the only handle a client needs.
type loginFlow struct {
	session providerauth.Session
	cancel  context.CancelFunc
}

// providerRow is one provider in a list_providers answer.
type providerRow struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Methods []string `json:"methods"`
	Ready   bool     `json:"ready"`
	Source  string   `json:"source"`
	// Current marks the provider that serves the current model, so a client can
	// tell whether the model it is using is signed in without knowing the catalog.
	Current bool `json:"current"`
	// DefaultModel is the model to switch to after signing in to this provider.
	DefaultModel string `json:"default_model,omitempty"`
}

// handleListProviders answers every registered provider and whether the
// server host is already signed in to it.
func (s *Server) handleListProviders(cmd proto.Command) proto.Response {
	all := providerauth.All()
	s.mu.Lock()
	model := s.cfg.Model
	s.mu.Unlock()
	serving, _ := providerauth.ForModel(model)
	rows := make([]providerRow, 0, len(all))
	for _, p := range all {
		st := providerauth.Resolve(p, p.ConfigKeyFrom(s.cfg), os.Getenv, s.home)
		methods := make([]string, 0, len(p.Methods))
		for _, m := range p.Methods {
			methods = append(methods, string(m))
		}
		rows = append(rows, providerRow{
			ID: p.ID, Name: p.Name, Methods: methods, Ready: st.Ready, Source: st.Source,
			Current: p.ID == serving.ID, DefaultModel: p.DefaultModel,
		})
	}
	return okResponse(cmd, rows)
}

// pickMethod chooses the method a login uses: the one named, or the
// provider's only one.
func pickMethod(p providerauth.Provider, named string) (providerauth.Method, error) {
	if named == "" {
		if len(p.Methods) != 1 {
			return "", fmt.Errorf("login: %s has several methods; name one in args", p.ID)
		}
		return p.Methods[0], nil
	}
	for _, m := range p.Methods {
		if string(m) == named {
			return m, nil
		}
	}
	return "", fmt.Errorf("login: %s does not support the %q method", p.ID, named)
}

// scrub removes a credential from an error message, so a failing save can
// never echo the key it was given back to a client.
func scrub(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "[redacted]")
}

// handleLogin signs the server host in. An API key is saved at once; an
// interactive method starts a flow that finishes with one login_end event.
func (s *Server) handleLogin(cmd proto.Command) proto.Response {
	if cmd.Name == "" {
		return failResponse(cmd, proto.CodeBadRequest, "login: name is required")
	}
	p, ok := providerauth.Get(cmd.Name)
	if !ok {
		return failResponse(cmd, proto.CodeNotFound, "login: unknown provider "+cmd.Name)
	}
	method, err := pickMethod(p, cmd.Args)
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	if method == providerauth.APIKey {
		return s.loginWithKey(cmd, p)
	}
	return s.loginInteractive(cmd, p, method)
}

// loginWithKey saves a pasted key.
func (s *Server) loginWithKey(cmd proto.Command, p providerauth.Provider) proto.Response {
	key := strings.TrimSpace(cmd.Text)
	if key == "" {
		return failResponse(cmd, proto.CodeBadRequest, "login: the key is empty")
	}
	if _, err := p.Save(s.home, providerauth.Credential{Method: providerauth.APIKey, Key: key}); err != nil {
		return failResponse(cmd, proto.CodeInternal, scrub("login: could not save the credential: "+err.Error(), key))
	}
	return okResponse(cmd, map[string]any{"provider": p.ID, "saved": true})
}

// loginInteractive begins a browser or device flow and waits for it in the
// background. The server never opens a browser: its host is usually headless,
// so the client is handed the URL and opens it where the user is.
func (s *Server) loginInteractive(cmd proto.Command, p providerauth.Provider, method providerauth.Method) proto.Response {
	begin, ok := p.Begin[method]
	if !ok {
		return failResponse(cmd, proto.CodeBadRequest, p.ID+" cannot sign in with "+string(method))
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Claim the slot before Begin, which does I/O, so two concurrent logins
	// for one provider cannot both start.
	flow := &loginFlow{cancel: cancel}
	if !s.claimLogin(p.ID, flow) {
		cancel()
		return failResponse(cmd, proto.CodeBusy, "login: a sign-in for "+p.ID+" is already in progress")
	}
	session, err := begin(ctx, providerauth.Config{Home: s.home, Env: os.Getenv})
	if err != nil {
		s.releaseLogin(p.ID, flow)
		cancel()
		return failResponse(cmd, proto.CodeProviderError, "login: "+err.Error())
	}
	// Publish the session and count the goroutine in one locked step that also
	// checks the slot survived Begin. If Close or a cancel took it meanwhile,
	// nothing may start, or loginWG.Add could race a Wait that already ran.
	s.mu.Lock()
	if s.logins[p.ID] != flow {
		s.mu.Unlock()
		_ = session.Close()
		cancel()
		return failResponse(cmd, proto.CodeBusy, "login: the sign-in for "+p.ID+" was cancelled")
	}
	flow.session = session
	s.loginWG.Add(1)
	s.mu.Unlock()
	go s.awaitLogin(ctx, p, flow)
	return okResponse(cmd, map[string]any{
		"provider": p.ID, "method": string(method),
		"url": session.URL(), "code": session.Code(), "prompt": session.Prompt(),
	})
}

// claimLogin records a flow for a provider, or reports one is already open.
func (s *Server) claimLogin(id string, f *loginFlow) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.logins[id]; busy {
		return false
	}
	s.logins[id] = f
	return true
}

// releaseLogin frees a provider's slot if f still holds it.
func (s *Server) releaseLogin(id string, f *loginFlow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logins[id] == f {
		delete(s.logins, id)
	}
}

// awaitLogin waits for the flow, saves its credential, and emits login_end. A
// cancelled flow has already been released by login_cancel, so it stays silent.
func (s *Server) awaitLogin(ctx context.Context, p providerauth.Provider, f *loginFlow) {
	defer s.loginWG.Done()
	cred, err := f.session.Wait(ctx)
	if ctx.Err() != nil {
		return
	}
	if err == nil {
		_, err = p.Save(s.home, cred)
	}
	s.releaseLogin(p.ID, f)
	_ = f.session.Close()
	ev := proto.Event{Type: proto.EventLoginEnd, ID: p.ID}
	if err != nil {
		ev.Error = &proto.Error{Code: proto.CodeProviderError, Message: scrub(err.Error(), cred.Key)}
	}
	s.emit(ev)
}

// openFlow returns the open sign-in for cmd.Name, or the not_found response to
// send. A flow still inside Begin has no session yet and does not count as open.
func (s *Server) openFlow(cmd proto.Command) (*loginFlow, *proto.Response) {
	s.mu.Lock()
	f := s.logins[cmd.Name]
	open := f != nil && f.session != nil
	s.mu.Unlock()
	if !open {
		resp := failResponse(cmd, proto.CodeNotFound, cmd.Type+": no sign-in in progress for "+cmd.Name)
		return nil, &resp
	}
	return f, nil
}

// handleLoginSubmit feeds a pasted redirect URL to the open flow.
func (s *Server) handleLoginSubmit(cmd proto.Command) proto.Response {
	f, notFound := s.openFlow(cmd)
	if notFound != nil {
		return *notFound
	}
	if err := f.session.Submit(cmd.Text); err != nil {
		return failResponse(cmd, proto.CodeBadRequest, scrub("login_submit: "+err.Error(), cmd.Text))
	}
	return okResponse(cmd, nil)
}

// handleLoginCancel abandons the open flow without emitting login_end.
func (s *Server) handleLoginCancel(cmd proto.Command) proto.Response {
	f, notFound := s.openFlow(cmd)
	if notFound != nil {
		return *notFound
	}
	s.releaseLogin(cmd.Name, f)
	f.cancel()
	_ = f.session.Close()
	return okResponse(cmd, nil)
}

// handleLogout deletes the stored credential and reports any source that still
// signs the host in (an environment variable or a hand-edited config key).
func (s *Server) handleLogout(cmd proto.Command) proto.Response {
	if cmd.Name == "" {
		return failResponse(cmd, proto.CodeBadRequest, "logout: name is required")
	}
	p, ok := providerauth.Get(cmd.Name)
	if !ok {
		return failResponse(cmd, proto.CodeNotFound, "logout: unknown provider "+cmd.Name)
	}
	if p.Delete == nil {
		return failResponse(cmd, proto.CodeUnsupported, "logout: "+p.ID+" has no stored credential")
	}
	if err := p.Delete(s.home); err != nil && !errors.Is(err, os.ErrNotExist) {
		return failResponse(cmd, proto.CodeInternal, "logout: "+err.Error())
	}
	st := providerauth.Resolve(p, p.ConfigKeyFrom(s.cfg), os.Getenv, s.home)
	return okResponse(cmd, map[string]any{"provider": p.ID, "remaining": st.Source})
}

// closeLogins cancels every open flow and waits for its goroutine, so Close
// leaves nothing running.
func (s *Server) closeLogins() {
	type open struct {
		cancel  context.CancelFunc
		session providerauth.Session
	}
	s.mu.Lock()
	flows := make([]open, 0, len(s.logins))
	for _, f := range s.logins {
		flows = append(flows, open{f.cancel, f.session})
	}
	s.logins = map[string]*loginFlow{}
	s.mu.Unlock()
	for _, f := range flows {
		f.cancel()
		if f.session != nil {
			_ = f.session.Close()
		}
	}
	s.loginWG.Wait()
}
