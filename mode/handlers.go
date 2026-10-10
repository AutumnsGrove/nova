package mode

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
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

// handleListModels returns the model catalog, optionally filtered by a query.
func (s *Server) handleListModels(cmd proto.Command) proto.Response {
	models := provider.Models()
	query := strings.ToLower(strings.TrimSpace(cmd.Text))
	if query != "" {
		filtered := models[:0]
		for _, m := range models {
			if strings.Contains(strings.ToLower(m.ID), query) || strings.Contains(strings.ToLower(m.Name), query) {
				filtered = append(filtered, m)
			}
		}
		models = filtered
	}
	return okResponse(cmd, map[string]any{"models": models})
}

// handleSessionList lists sessions for the working directory, newest first.
func (s *Server) handleSessionList(cmd proto.Command) proto.Response {
	infos, err := session.List(session.DefaultDir(s.cwd))
	if err != nil {
		return okResponse(cmd, map[string]any{"sessions": []session.Info{}})
	}
	return okResponse(cmd, map[string]any{"sessions": infos})
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
