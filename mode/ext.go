package mode

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/bundled/subagent"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
)

// buildExtensionHost constructs the host that loads subprocess extensions. It
// is not started here; New starts it in the background.
func (s *Server) buildExtensionHost() *ext.Host {
	extra := map[string]any{"tools": s.tools}
	for k, v := range s.opt.Extra {
		extra[k] = v
	}
	return ext.NewHost(ext.HostOptions{
		Dirs:        extensionDirs(s.cfg, s.cwd, s.home),
		Disabled:    s.cfg.Extensions.Disable,
		Registry:    s.tools,
		Session:     sessionBridge{s},
		SessionID:   s.sessionID(),
		UI:          uiBridge{s},
		Complete:    s.complete,
		CompleteMax: s.cfg.Extensions.CompleteMaxPerRun,
		Emit:        s.emit,
		NovaVersion: s.opt.Version,
		CWD:         s.cwd,
		Mode:        cmp.Or(s.opt.Mode, "rpc"), // "rpc" is the protocol default
		Env:         os.Environ,
		ScrubEnv:    append(providerauth.CredentialEnv(), s.cfg.Extensions.ScrubEnv...),
		PassEnv:     s.cfg.Extensions.PassEnv,
		Config:      s.cfg,
		Ledger:      s.ledger,
		Extra:       extra,
		Spawn:       s.spawn,
		HasUI:       s.supportsUI,
	})
}

// spawn runs a child agent loop for the bundled subagent extension. The child
// inherits the permission policy, the approval path, and the parent's
// tool_call and tool_result hooks.
func (s *Server) spawn(ctx context.Context, r ext.SpawnRequest) (*ext.SpawnResult, error) {
	hooks := s.ext.Hooks()
	s.mu.Lock()
	model := s.model
	s.mu.Unlock()
	sp := &subagent.Spawner{
		Provider:   s.prov,
		Model:      model,
		Registry:   s.tools,
		Policy:     &s.policy,
		Ask:        s.ask,
		ToolCall:   hooks.ToolCall,
		ToolResult: hooks.ToolResult,
		WorkDir:    s.cwd,
		Roots:      []string{s.cwd},
		SessionID:  s.sessionID(),
		Complete:   s.complete,
		Ledger:     s.ledger,
		MaxDepth:   s.cfg.Subagent.MaxDepth,
		MaxTokens:  s.cfg.MaxTokens,
		Emit:       s.emit,
	}
	return sp.Spawn(ctx, r)
}

// startExtensions starts the host in the background and reports a per-extension
// failure as an extension_error event. extStarted closes when it is done, so
// Close never shuts the host down while it is still starting processes.
func (s *Server) startExtensions() {
	defer close(s.extStarted)
	for _, err := range s.ext.Start(context.Background()) {
		s.emitExtensionError(err)
	}
}

// emitExtensionError reports a failure to load or reload one extension.
func (s *Server) emitExtensionError(err error) {
	s.emit(proto.Event{Type: proto.EventExtensionError, Error: &proto.Error{Code: proto.CodeInternal, Message: err.Error()}})
}

// extensionDirs lists the extension directories to load: explicit config dirs,
// then the user root, then the project root when it is trusted.
func extensionDirs(cfg *config.Config, cwd, home string) []string {
	var dirs []string
	for _, d := range cfg.Extensions.Dirs {
		dirs = append(dirs, extensionsIn(d)...)
	}
	dirs = append(dirs, extensionsIn(filepath.Join(config.UserConfigDir(home), "extensions"))...)
	if config.NewTrustStore(trustPath(home)).IsTrusted(cwd) {
		dirs = append(dirs, extensionsIn(filepath.Join(config.ProjectConfigDir(findRoot(cwd)), "extensions"))...)
	}
	return dirs
}

// complete is the nested model call a tool gets as Context.Complete. An empty
// request picks up the current model; a request that names a catalog ModelID is
// resolved to that model's slug, so a describe tool can ask for a vision
// fallback. The usage comes back to the tool, which reports it with AddUsage.
func (s *Server) complete(ctx context.Context, req provider.Request) (*provider.Response, error) {
	s.mu.Lock()
	if req.Model == "" && req.ModelID == "" {
		req.Model = s.model
		req.ModelID = s.cfg.Model
	} else if req.Model == "" {
		req.Model = modelSlug(req.ModelID)
	}
	req.MaxTokens = cmp.Or(req.MaxTokens, s.cfg.MaxTokens)
	s.mu.Unlock()
	return s.prov.Stream(ctx, req, nil)
}

// sessionBridge adapts the server's session store to ext.SessionAPI.
type sessionBridge struct{ s *Server }

func (b sessionBridge) AppendEntry(kind string, data json.RawMessage, keep bool) error {
	store := b.s.sessionStore()
	if store == nil {
		return nil
	}
	_, err := store.Append(session.Entry{Type: session.TypeCustom, Kind: kind, Data: data, Keep: keep})
	return err
}

func (b sessionBridge) Entries(kind string) []json.RawMessage {
	store := b.s.sessionStore()
	if store == nil {
		return nil
	}
	entries, err := store.Branch("")
	if err != nil {
		return nil
	}
	var out []json.RawMessage
	for _, e := range entries {
		if e.Type == session.TypeCustom && e.Kind == kind && len(e.Data) > 0 {
			out = append(out, e.Data)
		}
	}
	return out
}

// uiBridge adapts the server's UI plumbing to ext.UIBridge.
type uiBridge struct{ s *Server }

func (b uiBridge) Request(ctx context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	if !b.s.supportsUI("ui." + req.Method) {
		return proto.UIResponse{}, errUnsupportedUI
	}
	return b.s.callUI(ctx, req)
}

// Notify queues a fire-and-forget request for the client. It never blocks the
// calling extension: when the client is so far behind that the queue is full,
// the request is dropped, as a lagging event subscriber is.
func (b uiBridge) Notify(req proto.UIRequest) {
	if !b.s.supportsUI("ui." + req.Method) {
		return
	}
	b.s.startNotifier()
	select {
	case b.s.notifyCh <- req:
	default:
	}
}

// notifyQueueSize bounds the fire-and-forget requests waiting for the client,
// and notifyTimeout bounds how long the client gets to take one.
const (
	notifyQueueSize = 64
	notifyTimeout   = time.Second
)

// startNotifier starts the single goroutine that delivers fire-and-forget
// requests, once, unless the server is closed. One goroutine keeps updates to
// the same status or widget key in the order the extension sent them.
func (s *Server) startNotifier() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.notifying {
		return
	}
	s.notifying = true
	go s.notifyLoop()
}

// notifyLoop delivers queued requests until the server closes. Close does not
// wait for it: a handler stuck writing to a dead client must not hang Close.
func (s *Server) notifyLoop() {
	for {
		select {
		case <-s.life.Done():
			return
		case req := <-s.notifyCh:
			s.deliverNotify(req)
		}
	}
}

// deliverNotify hands one request to the current UI handler. A panicking
// handler loses that request, not the loop.
func (s *Server) deliverNotify(req proto.UIRequest) {
	defer diag.Catch("ui notify")
	s.uiMu.Lock()
	h := s.uiHandler
	s.uiMu.Unlock()
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.life, notifyTimeout)
	defer cancel()
	_, _ = h(ctx, req)
}

// errUnsupportedUI reports a UI method the attached client cannot answer.
var errUnsupportedUI = errors.New("the UI client cannot answer this method")

// hasExtensionCommand reports whether a running extension owns name.
func (s *Server) hasExtensionCommand(name string) bool {
	return slices.ContainsFunc(s.ext.Commands(), func(c ext.Command) bool { return c.Name == name })
}

// isExtensionDir reports whether dir holds an extension manifest.
func isExtensionDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "extension.yaml"))
	return err == nil
}

// extensionsIn returns the immediate extension directories below root. A root
// that is itself an extension is returned as-is.
func extensionsIn(root string) []string {
	if root == "" {
		return nil
	}
	if isExtensionDir(root) {
		return []string{root}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if dir := filepath.Join(root, e.Name()); isExtensionDir(dir) {
			out = append(out, dir)
		}
	}
	return out
}
