package mode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		Mode:        modeName(s.opt.Mode),
		Env:         os.Environ,
		ScrubEnv:    s.cfg.Extensions.ScrubEnv,
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
	sp := &subagent.Spawner{
		Provider:   s.prov,
		Model:      s.model,
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
		Emit:       s.emit,
	}
	return sp.Spawn(ctx, r)
}

// startExtensions starts the host in the background and reports a per-extension
// failure as an extension_error event.
func (s *Server) startExtensions() {
	if s.ext == nil {
		return
	}
	for _, err := range s.ext.Start(context.Background()) {
		s.emit(proto.Event{Type: proto.EventExtensionError, Error: &proto.Error{Code: proto.CodeInternal, Message: err.Error()}})
	}
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

// complete performs one non-streaming provider call for model:complete.
func (s *Server) complete(ctx context.Context, req provider.Request) (*provider.Response, error) {
	s.mu.Lock()
	req.Model = s.model
	s.mu.Unlock()
	return s.prov.Stream(ctx, req, nil)
}

// sessionBridge adapts the server's session store to ext.SessionAPI.
type sessionBridge struct{ s *Server }

func (b sessionBridge) AppendEntry(kind string, data json.RawMessage, keep bool) error {
	if b.s.store == nil {
		return nil
	}
	_, err := b.s.store.Append(session.Entry{Type: session.TypeCustom, Kind: kind, Data: data, Keep: keep})
	return err
}

func (b sessionBridge) Entries(kind string) []json.RawMessage {
	if b.s.store == nil {
		return nil
	}
	entries, err := b.s.store.Branch("")
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

func (b uiBridge) Notify(req proto.UIRequest) {
	if !b.s.supportsUI("ui." + req.Method) {
		return
	}
	b.s.uiMu.Lock()
	h := b.s.uiHandler
	b.s.uiMu.Unlock()
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { defer diag.Catch("ui notify"); _, _ = h(ctx, req) }()
}

// errUnsupportedUI reports a UI method the attached client cannot answer.
var errUnsupportedUI = errors.New("the UI client cannot answer this method")

// extensionCommands returns the running extensions' slash commands.
func (s *Server) extensionCommands() []ext.Command {
	if s.ext == nil {
		return nil
	}
	return s.ext.Commands()
}

// hasExtensionCommand reports whether a running extension owns name.
func (s *Server) hasExtensionCommand(name string) bool {
	for _, c := range s.extensionCommands() {
		if c.Name == name {
			return true
		}
	}
	return false
}

// modeName maps an empty mode to "rpc", the protocol default.
func modeName(mode string) string {
	if mode == "" {
		return "rpc"
	}
	return mode
}

// extensionsIn returns the immediate extension directories below root. A root
// that is itself an extension is returned as-is.
func extensionsIn(root string) []string {
	if root == "" {
		return nil
	}
	if pathExists(filepath.Join(root, "extension.yaml")) {
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
		dir := filepath.Join(root, e.Name())
		if pathExists(filepath.Join(dir, "extension.yaml")) {
			out = append(out, dir)
		}
	}
	return out
}
