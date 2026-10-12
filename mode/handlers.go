package mode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/tool"
)

// builtinCommands are the slash commands handled in-process.
var builtinCommands = []string{"model", "thinking", "context", "compact", "new", "name", "cost", "help"}

// handleGetMessages returns the active branch as model messages.
func (s *Server) handleGetMessages(cmd proto.Command) proto.Response {
	if s.store == nil {
		return okResponse(cmd, map[string]any{"messages": []message.Message{}})
	}
	msgs, err := s.store.Messages(cmd.Entry)
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	return okResponse(cmd, map[string]any{"messages": msgs})
}

// modelInfo is one catalog entry as the protocol carries it. The field names
// are the wire contract for list_models.
type modelInfo struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Provider      string  `json:"provider"`
	ContextWindow int     `json:"context_window"`
	InPerM        float64 `json:"in_per_m"`
	OutPerM       float64 `json:"out_per_m"`
	Current       bool    `json:"current"`
}

// handleListModels returns the model catalog, optionally filtered by a query.
// The current model is marked so a picker can show it.
func (s *Server) handleListModels(cmd proto.Command) proto.Response {
	query := strings.ToLower(strings.TrimSpace(cmd.Text))
	s.mu.Lock()
	current := s.cfg.Model
	s.mu.Unlock()
	out := make([]modelInfo, 0)
	for _, m := range provider.Models() {
		if query != "" && !strings.Contains(strings.ToLower(m.ID), query) && !strings.Contains(strings.ToLower(m.Name), query) {
			continue
		}
		out = append(out, modelInfo{
			ID:            m.ID,
			Name:          m.Name,
			Provider:      m.Provider,
			ContextWindow: m.ContextWindow,
			InPerM:        m.InPerM,
			OutPerM:       m.OutPerM,
			Current:       m.ID == current,
		})
	}
	return okResponse(cmd, out)
}

// sessionInfo is one session row as the protocol carries it.
type sessionInfo struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Modified time.Time `json:"modified"`
	Entries  int       `json:"entries"`
	Current  bool      `json:"current"`
}

// handleSessionList lists sessions for the working directory, newest first.
func (s *Server) handleSessionList(cmd proto.Command) proto.Response {
	infos, err := session.List(session.DefaultDir(s.cwd))
	if err != nil {
		return okResponse(cmd, []sessionInfo{})
	}
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()
	out := make([]sessionInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, sessionInfo{
			ID:       info.ID,
			Path:     info.Path,
			Name:     info.Name,
			Modified: info.Modified,
			Entries:  info.Entries,
			Current:  info.Path == path,
		})
	}
	return okResponse(cmd, out)
}

// handleGetExtensions returns the extension host's supervision status, or an
// empty list when there is no host.
func (s *Server) handleGetExtensions(cmd proto.Command) proto.Response {
	if s.ext == nil {
		return okResponse(cmd, []ext.Status{})
	}
	return okResponse(cmd, s.ext.Status())
}

// handleGetCommands lists built-in commands and prompt templates.
func (s *Server) handleGetCommands(cmd proto.Command) proto.Response {
	type commandInfo struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Kind        string `json:"kind"`
	}
	out := make([]commandInfo, 0, len(builtinCommands))
	for _, name := range builtinCommands {
		out = append(out, commandInfo{Name: name, Kind: "builtin"})
	}
	for _, tpl := range s.templates() {
		out = append(out, commandInfo{Name: tpl.Name, Description: tpl.Description, Kind: "template"})
	}
	for _, c := range s.extensionCommands() {
		out = append(out, commandInfo{Name: c.Name, Description: c.Description, Kind: "extension"})
	}
	return okResponse(cmd, map[string]any{"commands": out})
}

// templates loads prompt templates from the project and user directories.
func (s *Server) templates() []config.Template {
	root := findRoot(s.cwd)
	dirs := []string{
		config.PromptsDir(config.ProjectConfigDir(root)),
		config.PromptsDir(config.UserConfigDir(s.home)),
	}
	templates, _ := config.LoadTemplates(dirs)
	return templates
}

// handleRunCommand runs a built-in or expands a prompt template.
func (s *Server) handleRunCommand(cmd proto.Command) proto.Response {
	switch cmd.Name {
	case "help":
		return okResponse(cmd, map[string]any{"text": "commands: " + strings.Join(builtinCommands, ", ")})
	case "cost":
		s.mu.Lock()
		usage := s.usage
		s.mu.Unlock()
		return okResponse(cmd, map[string]any{"text": fmt.Sprintf("tokens: %d, cost: $%.4f", usage.TotalTokens, usage.CostUSD)})
	case "context":
		msgs := s.history()
		return okResponse(cmd, map[string]any{"text": fmt.Sprintf("estimated context: %d tokens", compaction.Estimate(msgs))})
	case "model":
		if strings.TrimSpace(cmd.Args) == "" {
			return okResponse(cmd, map[string]any{"text": s.cfg.Model})
		}
		return s.handleSetModel(proto.Command{Type: proto.CmdSetModel, ID: cmd.ID, Model: strings.TrimSpace(cmd.Args)})
	case "thinking":
		if strings.TrimSpace(cmd.Args) == "" {
			s.mu.Lock()
			level := s.think
			s.mu.Unlock()
			return okResponse(cmd, map[string]any{"text": level})
		}
		return s.handleSetThinking(proto.Command{Type: proto.CmdSetThinking, ID: cmd.ID, Level: strings.TrimSpace(cmd.Args)})
	case "name":
		return s.handleSessionName(proto.Command{Type: proto.CmdSessionName, ID: cmd.ID, Name: cmd.Args})
	case "compact":
		return s.handleCompact(proto.Command{Type: proto.CmdCompact, ID: cmd.ID})
	}
	for _, tpl := range s.templates() {
		if tpl.Name == cmd.Name {
			text := tpl.Expand(cmd.Args)
			if strings.TrimSpace(text) == "" {
				return failResponse(cmd, proto.CodeBadRequest, "template produced an empty prompt")
			}
			if s.agent.Running() {
				return failResponse(cmd, proto.CodeBusy, "a run is already in progress")
			}
			go s.run(text)
			return okResponse(cmd, nil)
		}
	}
	if s.ext != nil && s.hasExtensionCommand(cmd.Name) {
		out, err := s.ext.RunCommand(context.Background(), cmd.Name, cmd.Args)
		if err != nil {
			return failResponse(cmd, proto.CodeInternal, err.Error())
		}
		return okResponse(cmd, map[string]any{"text": out})
	}
	return failResponse(cmd, proto.CodeBadRequest, "unknown command "+cmd.Name)
}

// handleCompact runs compaction on demand.
func (s *Server) handleCompact(cmd proto.Command) proto.Response {
	if s.store == nil {
		return failResponse(cmd, proto.CodeBadRequest, "no session to compact")
	}
	out, err := compaction.Run(context.Background(), s.store, s.compactor, cmd.Instructions)
	if err != nil {
		if errors.Is(err, compaction.ErrNothingToCompact) {
			return failResponse(cmd, proto.CodeBadRequest, "nothing to compact")
		}
		return failResponse(cmd, proto.CodeInternal, "compaction failed: "+err.Error())
	}
	s.emit(proto.Event{
		Type:         proto.EventCompaction,
		Entry:        out.Entry.ID,
		TokensBefore: out.TokensBefore,
		TokensAfter:  out.TokensAfter,
	})
	return okResponse(cmd, nil)
}

// handleSessionName records a display name for the session.
func (s *Server) handleSessionName(cmd proto.Command) proto.Response {
	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		return failResponse(cmd, proto.CodeBadRequest, "session.name: name is required")
	}
	if s.store == nil {
		return failResponse(cmd, proto.CodeBadRequest, "no session")
	}
	_, err := s.store.Append(session.Entry{Type: session.TypeSessionInfo, Name: name})
	if err != nil {
		return failResponse(cmd, proto.CodeInternal, err.Error())
	}
	s.emitSession()
	return okResponse(cmd, nil)
}

// handleSessionNavigate moves the active leaf.
func (s *Server) handleSessionNavigate(cmd proto.Command) proto.Response {
	if s.store == nil {
		return failResponse(cmd, proto.CodeBadRequest, "no session")
	}
	if s.agent.Running() {
		return failResponse(cmd, proto.CodeBusy, "a run is in progress")
	}
	if err := s.store.SetLeaf(cmd.Entry); err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	s.emitSession()
	return okResponse(cmd, map[string]any{"leaf": s.store.Leaf()})
}

// handleSessionTree returns every entry as a nested tree.
func (s *Server) handleSessionTree(cmd proto.Command) proto.Response {
	if s.store == nil {
		return okResponse(cmd, map[string]any{"tree": []any{}})
	}
	return okResponse(cmd, map[string]any{"tree": s.tree("")})
}

// treeNode is one entry and its children.
type treeNode struct {
	Entry    session.Entry `json:"entry"`
	Children []*treeNode   `json:"children,omitempty"`
}

// tree builds the entry tree below parent.
func (s *Server) tree(parent string) []*treeNode {
	var out []*treeNode
	for _, id := range s.store.Children(parent) {
		entry, ok := s.store.Get(id)
		if !ok {
			continue
		}
		out = append(out, &treeNode{Entry: entry, Children: s.tree(id)})
	}
	return out
}

// emitSession emits a session_changed event.
func (s *Server) emitSession() {
	s.mu.Lock()
	id := s.sessionID()
	leaf := s.leafID()
	s.mu.Unlock()
	s.emit(proto.Event{Type: proto.EventSessionChanged, Session: id, Leaf: leaf})
}

// handleSessionNew starts a fresh session file.
func (s *Server) handleSessionNew(cmd proto.Command) proto.Response {
	if s.agent.Running() {
		return failResponse(cmd, proto.CodeBusy, "a run is in progress")
	}
	path := defaultSessionPath(s.cwd)
	if err := s.openSession(path); err != nil {
		return failResponse(cmd, proto.CodeInternal, err.Error())
	}
	s.emitSession()
	return okResponse(cmd, map[string]any{"id": s.sessionID(), "path": path})
}

// handleSessionResume opens an existing session by path or id.
func (s *Server) handleSessionResume(cmd proto.Command) proto.Response {
	if s.agent.Running() {
		return failResponse(cmd, proto.CodeBusy, "a run is in progress")
	}
	target := strings.TrimSpace(cmd.Session)
	if target == "" {
		return failResponse(cmd, proto.CodeBadRequest, "session.resume: id or path is required")
	}
	if _, err := os.Stat(target); err != nil {
		resolved, err := findSession(s.cwd, target)
		if err != nil {
			return failResponse(cmd, proto.CodeNotFound, err.Error())
		}
		target = resolved
	}
	if err := s.openSession(target); err != nil {
		return failResponse(cmd, proto.CodeInternal, err.Error())
	}
	s.emitSession()
	return okResponse(cmd, map[string]any{"id": s.sessionID(), "path": target})
}

// handleSessionFork copies the branch up to an entry into a new session.
func (s *Server) handleSessionFork(cmd proto.Command) proto.Response {
	if s.agent.Running() {
		return failResponse(cmd, proto.CodeBusy, "a run is in progress")
	}
	if s.store == nil {
		return failResponse(cmd, proto.CodeBadRequest, "no session")
	}
	dst := defaultSessionPath(s.cwd)
	forked, err := session.Fork(s.store, cmd.Entry, dst, session.Options{})
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	s.mu.Lock()
	old := s.store
	s.store = forked
	s.path = dst
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	s.emitSession()
	return okResponse(cmd, map[string]any{"id": s.sessionID(), "path": dst})
}

// openSession closes the current store and opens path.
func (s *Server) openSession(path string) error {
	store, err := session.Open(path, session.Options{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	old := s.store
	s.store = store
	s.path = path
	s.mem = nil
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// findSession resolves a session id to a path in cwd's session directory.
func findSession(cwd, id string) (string, error) {
	infos, err := session.List(session.DefaultDir(cwd))
	if err != nil {
		return "", err
	}
	for _, info := range infos {
		if info.ID == id {
			return info.Path, nil
		}
	}
	return "", fmt.Errorf("session %q not found", id)
}

// handleBash runs a user shell command and records it as a custom message.
func (s *Server) handleBash(cmd proto.Command) proto.Response {
	command := cmd.Command
	if strings.TrimSpace(command) == "" {
		return failResponse(cmd, proto.CodeBadRequest, "bash: command is required")
	}
	quiet := strings.HasPrefix(strings.TrimSpace(command), "!!")
	if quiet {
		command = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), "!!"))
	}
	shell, ok := s.pool["bash"]
	if !ok {
		return failResponse(cmd, proto.CodeBadRequest, "the bash tool is not active")
	}
	args, _ := json.Marshal(map[string]string{"command": command})
	res, err := shell.Run(context.Background(), args, &tool.Context{WorkDir: s.cwd, Roots: []string{s.cwd}})
	if err != nil {
		res = tool.Errorf("%v", err)
	}
	if !quiet && s.store != nil {
		_, _ = s.store.Append(session.Entry{Type: session.TypeCustomMessage, Content: res.Content, Display: true, InContext: false})
	}
	return okResponse(cmd, map[string]any{"content": res.Content, "is_error": res.IsError})
}

// handleReload rebuilds the system prompt after the active run finishes.
func (s *Server) handleReload(ctx context.Context, cmd proto.Command) proto.Response {
	for s.agent.Running() {
		if err := s.clock.Sleep(ctx, 10*time.Millisecond); err != nil {
			return failResponse(cmd, proto.CodeBadRequest, err.Error())
		}
	}
	s.mu.Lock()
	s.system = buildSystemPrompt(s.cfg, s.cwd, s.home, s.tools, s.clock.Now())
	s.mu.Unlock()
	if s.ext != nil {
		for _, err := range s.ext.Reload(ctx) {
			s.emit(proto.Event{Type: proto.EventExtensionError, Error: &proto.Error{Code: proto.CodeInternal, Message: err.Error()}})
		}
	}
	s.emitState()
	return okResponse(cmd, map[string]any{"reloaded": []string{"config", "skills", "templates", "extensions"}})
}

// transformContext keeps the leading system message current after a reload.
func (s *Server) transformContext(_ context.Context, msgs []message.Message) []message.Message {
	s.mu.Lock()
	system := s.system
	s.mu.Unlock()
	if system == "" || len(msgs) == 0 || msgs[0].Role != message.RoleSystem {
		return msgs
	}
	out := append([]message.Message(nil), msgs...)
	out[0] = message.System(system)
	return out
}
