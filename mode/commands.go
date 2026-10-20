package mode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/auth/providerauth"
	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/mode/attach"
	"github.com/AutumnsGrove/nova/proto"
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

// commandHandler runs one command. Every handler is a method on Server, so the
// tables below can name them as method expressions.
type commandHandler func(*Server, proto.Command) proto.Response

// ctxHandler is a commandHandler that can block, so it takes the caller's
// context: a disconnected client or a shutdown cancels it.
type ctxHandler func(*Server, context.Context, proto.Command) proto.Response

// handlers maps each protocol command to its handler.
var handlers = map[string]commandHandler{
	proto.CmdPrompt:          (*Server).handlePrompt,
	proto.CmdSteer:           (*Server).handleSteer,
	proto.CmdFollowUp:        (*Server).handleFollowUp,
	proto.CmdAbort:           (*Server).handleAbort,
	proto.CmdGetState:        (*Server).handleGetState,
	proto.CmdSetModel:        (*Server).handleSetModel,
	proto.CmdSetThinking:     (*Server).handleSetThinking,
	proto.CmdSetContextLimit: (*Server).handleSetContextLimit,
	proto.CmdSetTools:        (*Server).handleSetTools,
	proto.CmdGetMessages:     (*Server).handleGetMessages,
	proto.CmdGetCommands:     (*Server).handleGetCommands,
	proto.CmdListModels:      (*Server).handleListModels,
	proto.CmdSessionList:     (*Server).handleSessionList,
	proto.CmdGetExtensions:   (*Server).handleGetExtensions,
	proto.CmdSessionTree:     (*Server).handleSessionTree,
	proto.CmdSessionNavigate: (*Server).handleSessionNavigate,
	proto.CmdSessionName:     (*Server).handleSessionName,
	proto.CmdSessionNew:      (*Server).handleSessionNew,
	proto.CmdSessionResume:   (*Server).handleSessionResume,
	proto.CmdSessionFork:     (*Server).handleSessionFork,
	proto.CmdShutdown:        (*Server).handleShutdown,
	proto.CmdListProviders:   (*Server).handleListProviders,
	proto.CmdLogin:           (*Server).handleLogin,
	proto.CmdLoginSubmit:     (*Server).handleLoginSubmit,
	proto.CmdLoginCancel:     (*Server).handleLoginCancel,
	proto.CmdLogout:          (*Server).handleLogout,
}

// ctxHandlers maps the commands that can block to their handlers.
var ctxHandlers = map[string]ctxHandler{
	proto.CmdCompact:    (*Server).handleCompact,
	proto.CmdGetUsage:   (*Server).handleGetUsage,
	proto.CmdBash:       (*Server).handleBash,
	proto.CmdReload:     (*Server).handleReload,
	proto.CmdRunCommand: (*Server).handleRunCommand,
}

// dispatch routes a command to its handler.
func (s *Server) dispatch(ctx context.Context, cmd proto.Command) proto.Response {
	if h, ok := handlers[cmd.Type]; ok {
		return h(s, cmd)
	}
	if h, ok := ctxHandlers[cmd.Type]; ok {
		return h(s, ctx, cmd)
	}
	return failResponse(cmd, proto.CodeUnsupported, "unsupported command "+cmd.Type)
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
		if problem := s.signInProblem(); problem != "" {
			return failResponse(cmd, proto.CodeProviderError, problem)
		}
	}
	if cmd.Queue {
		return s.queueOrStart(cmd, msg)
	}
	if err := s.startRun(msg); err != nil {
		return startFailure(cmd, err)
	}
	return okResponse(cmd, nil)
}

// queueOrStart queues msg behind the active run, or starts a run when the
// agent is idle. It tries the queue first because a run can end between a
// Running check and the enqueue, which would answer a valid request with an
// error.
func (s *Server) queueOrStart(cmd proto.Command, msg message.Message) proto.Response {
	err := s.agent.FollowUp(msg)
	if errors.Is(err, agent.ErrNotRunning) {
		if err := s.startRun(msg); err != nil {
			return startFailure(cmd, err)
		}
		return okResponse(cmd, nil)
	}
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
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
	return s.queueOrStart(cmd, msg)
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
		"session":          storeID(s.store),
		"path":             s.path,
		"leaf":             storeLeaf(s.store),
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
	// The compaction trigger is the smaller of the user's limit and the model's
	// own window, so it must follow the model.
	s.compactor.Settings.ModelWindow = modelWindow(name)
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
	// The lock also serialises two set_tools commands, which would otherwise
	// interleave their unregister and register steps.
	s.mu.Lock()
	err := registerTools(s.tools, s.pool, active)
	if err == nil {
		s.active = append([]string(nil), active...)
		s.cfg.Tools.Active = append([]string(nil), active...)
	}
	s.mu.Unlock()
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	s.emitState()
	return okResponse(cmd, nil)
}

// handleShutdown aborts any run and closes the session.
func (s *Server) handleShutdown(cmd proto.Command) proto.Response {
	s.agent.Abort()
	if err := s.Close(); err != nil {
		return failResponse(cmd, proto.CodeInternal, "shutdown: "+err.Error())
	}
	return okResponse(cmd, nil)
}

// recordEntry appends a non-message entry to the session store. A failed append
// is reported as an error event: the entry is bookkeeping for a change that
// already took effect, so the command that caused it still succeeds.
func (s *Server) recordEntry(entry session.Entry) {
	store := s.sessionStore()
	if store == nil {
		return
	}
	if _, err := store.Append(entry); err != nil {
		s.emit(proto.Event{Type: proto.EventError, Error: &proto.Error{Code: proto.CodeInternal, Message: "session entry was not recorded: " + err.Error()}})
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

// busyResponse refuses a command that cannot run while the agent does.
func busyResponse(cmd proto.Command) proto.Response {
	return failResponse(cmd, proto.CodeBusy, "a run is already in progress")
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
