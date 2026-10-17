package mode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/mode/attach"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
)

// Handle runs one protocol command. It never panics: a panic becomes an
// internal error response and an error event.
func (s *Server) Handle(ctx context.Context, cmd proto.Command) proto.Response {
	return s.recoverResponse(cmd, func() proto.Response { return s.dispatch(ctx, cmd) })
}

// recoverResponse runs fn and turns a panic into an internal error response.
func (s *Server) recoverResponse(cmd proto.Command, fn func() proto.Response) (resp proto.Response) {
	defer func() {
		if r := recover(); r != nil {
			_, _ = diag.Report(r, "command "+cmd.Type)
			s.emit(proto.Event{Type: proto.EventError, Error: &proto.Error{Code: proto.CodeInternal, Message: fmt.Sprintf("panic: %v", r)}})
			resp = failResponse(cmd, proto.CodeInternal, fmt.Sprintf("internal error: %v", r))
		}
	}()
	return fn()
}

// dispatch routes a command to its handler.
func (s *Server) dispatch(ctx context.Context, cmd proto.Command) proto.Response {
	switch cmd.Type {
	case proto.CmdPrompt:
		return s.handlePrompt(cmd)
	case proto.CmdSteer:
		return s.handleSteer(cmd)
	case proto.CmdFollowUp:
		return s.handleFollowUp(cmd)
	case proto.CmdAbort:
		return s.handleAbort(cmd)
	case proto.CmdGetState:
		return s.handleGetState(cmd)
	case proto.CmdSetModel:
		return s.handleSetModel(cmd)
	case proto.CmdSetThinking:
		return s.handleSetThinking(cmd)
	case proto.CmdSetContextLimit:
		return s.handleSetContextLimit(cmd)
	case proto.CmdSetTools:
		return s.handleSetTools(cmd)
	case proto.CmdCompact:
		return s.handleCompact(cmd)
	case proto.CmdGetMessages:
		return s.handleGetMessages(cmd)
	case proto.CmdGetCommands:
		return s.handleGetCommands(cmd)
	case proto.CmdListModels:
		return s.handleListModels(cmd)
	case proto.CmdSessionList:
		return s.handleSessionList(cmd)
	case proto.CmdGetExtensions:
		return s.handleGetExtensions(cmd)
	case proto.CmdGetUsage:
		return s.handleGetUsage(cmd)
	case proto.CmdSessionTree:
		return s.handleSessionTree(cmd)
	case proto.CmdSessionNavigate:
		return s.handleSessionNavigate(cmd)
	case proto.CmdSessionName:
		return s.handleSessionName(cmd)
	case proto.CmdSessionNew:
		return s.handleSessionNew(cmd)
	case proto.CmdSessionResume:
		return s.handleSessionResume(cmd)
	case proto.CmdSessionFork:
		return s.handleSessionFork(cmd)
	case proto.CmdBash:
		return s.handleBash(cmd)
	case proto.CmdReload:
		return s.handleReload(ctx, cmd)
	case proto.CmdRunCommand:
		return s.handleRunCommand(cmd)
	case proto.CmdShutdown:
		return s.handleShutdown(cmd)
	case proto.CmdListProviders:
		return s.handleListProviders(cmd)
	case proto.CmdLogin:
		return s.handleLogin(cmd)
	case proto.CmdLoginSubmit:
		return s.handleLoginSubmit(cmd)
	case proto.CmdLoginCancel:
		return s.handleLoginCancel(cmd)
	case proto.CmdLogout:
		return s.handleLogout(cmd)
	default:
		return failResponse(cmd, proto.CodeUnsupported, "unsupported command "+cmd.Type)
	}
}

// handlePrompt accepts a prompt and starts a run in the background.
func (s *Server) handlePrompt(cmd proto.Command) proto.Response {
	msg, err := s.commandMessage(cmd.Text, cmd.Attachments)
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	if strings.TrimSpace(msg.Content) == "" && !msg.HasImages() {
		return failResponse(cmd, proto.CodeBadRequest, "prompt: text is required")
	}
	if s.usesAuth {
		if msg := s.signInProblem(); msg != "" {
			return failResponse(cmd, proto.CodeProviderError, msg)
		}
	}
	if s.agent.Running() {
		if !cmd.Queue {
			return failResponse(cmd, proto.CodeBusy, "a run is already in progress")
		}
		if err := s.agent.FollowUp(msg); err != nil {
			return failResponse(cmd, proto.CodeBadRequest, err.Error())
		}
		return okResponse(cmd, nil)
	}
	if err := s.startRun(msg); err != nil {
		return startFailure(cmd, err)
	}
	return okResponse(cmd, nil)
}

// signInProblem returns why the current model cannot be used yet, or "" when
// the provider that serves it is signed in. The check follows the model, so a
// user who signed in only to OpenCode Go is not asked for an OpenRouter key.
func (s *Server) signInProblem() string {
	s.mu.Lock()
	model := s.cfg.Model
	s.mu.Unlock()
	p, ok := providerauth.ForModel(model)
	if !ok {
		return ""
	}
	if providerauth.Resolve(p, p.ConfigKeyFrom(s.cfg), os.Getenv, s.home).Ready {
		return ""
	}
	login, forName := "nova login", ""
	if p.ID != "openrouter" {
		login, forName = "nova login "+p.ID, " for "+p.Name
	}
	env := ""
	if len(p.EnvVars) > 0 {
		env = " or set " + p.EnvVars[0]
	}
	return `no API key` + forName + `: run "` + login + `"` + env
}

// handleSteer queues a steering message for the active run.
func (s *Server) handleSteer(cmd proto.Command) proto.Response {
	if !s.agent.Running() {
		return failResponse(cmd, proto.CodeBadRequest, "not running")
	}
	msg, err := s.commandMessage(cmd.Text, cmd.Attachments)
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	if err := s.agent.Steer(msg); err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	return okResponse(cmd, nil)
}

// handleFollowUp queues work after the active run, or starts one when idle.
func (s *Server) handleFollowUp(cmd proto.Command) proto.Response {
	msg, err := s.commandMessage(cmd.Text, cmd.Attachments)
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	if s.agent.Running() {
		if err := s.agent.FollowUp(msg); err != nil {
			return failResponse(cmd, proto.CodeBadRequest, err.Error())
		}
		return okResponse(cmd, nil)
	}
	if err := s.startRun(msg); err != nil {
		return startFailure(cmd, err)
	}
	return okResponse(cmd, nil)
}

// commandMessage builds a prompt, steer, or follow-up message from a command,
// using the selected model's image capability.
func (s *Server) commandMessage(text string, atts []proto.Attachment) (message.Message, error) {
	atts, err := s.resolveAttachments(atts)
	if err != nil {
		return message.Message{}, err
	}
	s.mu.Lock()
	accepts := attach.Accepts(modelModalities(s.cfg.Model))
	s.mu.Unlock()
	return attach.Message(text, atts, accepts)
}

// handleAbort cancels the active run and returns the queued messages.
func (s *Server) handleAbort(cmd proto.Command) proto.Response {
	steering, followUp := s.agent.Abort()
	return okResponse(cmd, map[string]any{"steering": steering, "follow_up": followUp})
}

// handleGetState reports the server's current state.
func (s *Server) handleGetState(cmd proto.Command) proto.Response {
	msgs := s.history()
	s.mu.Lock()
	settings := s.compactor.Settings
	settings.ContextLimit = s.limit
	state := map[string]any{
		"model":            s.cfg.Model,
		"thinking":         s.think,
		"context_limit":    s.limit,
		"session":          s.sessionID(),
		"path":             s.path,
		"leaf":             s.leafID(),
		"running":          s.agent.Running(),
		"context_tokens":   compaction.Estimate(msgs),
		"effective_limit":  compaction.EffectiveLimit(settings),
		"usage":            s.usage,
		"cwd":              s.cwd,
		"input_modalities": modelModalities(s.cfg.Model),
	}
	steering, followUp := s.agent.Queued()
	state["steering"] = steering
	state["follow_up"] = followUp
	s.mu.Unlock()
	return okResponse(cmd, state)
}

// handleSetModel switches the model used by the next request.
func (s *Server) handleSetModel(cmd proto.Command) proto.Response {
	name := strings.TrimSpace(cmd.Model)
	if name == "" {
		return failResponse(cmd, proto.CodeBadRequest, "set_model: model is required")
	}
	slug := modelSlug(name)
	s.mu.Lock()
	s.model = slug
	s.compactor.Model = slug
	s.cfg.Model = name
	s.mu.Unlock()
	diag.SetModel(name)
	s.recordEntry(session.Entry{Type: session.TypeModelChange, Model: name})
	s.emitState()
	return okResponse(cmd, nil)
}

// handleSetThinking switches the reasoning effort.
func (s *Server) handleSetThinking(cmd proto.Command) proto.Response {
	switch cmd.Level {
	case "off", "low", "medium", "high":
	default:
		return failResponse(cmd, proto.CodeBadRequest, "set_thinking: level must be off, low, medium, or high")
	}
	s.mu.Lock()
	s.think = cmd.Level
	s.cfg.Thinking = cmd.Level
	s.mu.Unlock()
	s.emitState()
	return okResponse(cmd, nil)
}

// handleSetContextLimit changes the compaction trigger.
func (s *Server) handleSetContextLimit(cmd proto.Command) proto.Response {
	if cmd.Tokens == nil {
		return failResponse(cmd, proto.CodeBadRequest, "set_context_limit: tokens is required")
	}
	if *cmd.Tokens < 0 {
		return failResponse(cmd, proto.CodeBadRequest, "set_context_limit: tokens must not be negative")
	}
	s.mu.Lock()
	s.limit = *cmd.Tokens
	s.cfg.ContextLimit = *cmd.Tokens
	s.mu.Unlock()
	s.emitState()
	return okResponse(cmd, nil)
}

// handleSetTools replaces the active tool set.
func (s *Server) handleSetTools(cmd proto.Command) proto.Response {
	active := cmd.Active
	if len(active) == 0 {
		active = defaultToolNames
	}
	if err := registerTools(s.tools, s.pool, active); err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	s.mu.Lock()
	s.active = append([]string(nil), active...)
	s.cfg.Tools.Active = append([]string(nil), active...)
	s.mu.Unlock()
	s.emitState()
	return okResponse(cmd, nil)
}

// handleShutdown aborts any run and closes the session.
func (s *Server) handleShutdown(cmd proto.Command) proto.Response {
	s.agent.Abort()
	_ = s.Close()
	return okResponse(cmd, nil)
}

// leafID returns the active session leaf.
func (s *Server) leafID() string {
	if s.store == nil {
		return ""
	}
	return s.store.Leaf()
}

// recordEntry appends a non-message entry to the session store.
func (s *Server) recordEntry(entry session.Entry) {
	s.mu.Lock()
	store := s.store
	s.mu.Unlock()
	if store != nil {
		_, _ = store.Append(entry)
	}
}

// emitState emits a state_changed event carrying the fields a UI needs.
func (s *Server) emitState() {
	s.mu.Lock()
	state := map[string]any{
		"model":            s.cfg.Model,
		"thinking":         s.think,
		"context_limit":    s.limit,
		"tools":            s.active,
		"input_modalities": modelModalities(s.cfg.Model),
	}
	s.mu.Unlock()
	raw, _ := json.Marshal(state)
	s.emit(proto.Event{Type: proto.EventStateChanged, State: raw})
}

// modelModalities returns a catalog model's declared input kinds, or nil when
// the model is unknown (which the protocol treats as text only). It does not
// take s.mu, so callers already holding it can use it.
func modelModalities(id string) []string {
	if m, ok := provider.GetModel(id); ok {
		return m.InputModalities
	}
	return nil
}

// okResponse builds a successful response with optional JSON data.
func okResponse(cmd proto.Command, data any) proto.Response {
	resp := proto.Response{Type: proto.FrameResponse, ID: cmd.ID, Command: cmd.Type, OK: true}
	if data != nil {
		if raw, err := json.Marshal(data); err == nil {
			resp.Data = raw
		}
	}
	return resp
}

// failResponse builds an error response.
func failResponse(cmd proto.Command, code, message string) proto.Response {
	return proto.Response{
		Type:    proto.FrameResponse,
		ID:      cmd.ID,
		Command: cmd.Type,
		OK:      false,
		Error:   &proto.Error{Code: code, Message: message},
	}
}
