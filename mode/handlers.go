package mode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/mode/providers"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/tool"
)

// builtinCommands are the slash commands handled in-process.
var builtinCommands = []string{"model", "thinking", "context", "compact", "new", "clear", "name", "cost", "help", "reload", "quit", "exit"}

// handleGetMessages returns the active branch as model messages. Without a
// session store the transcript is the in-memory one, which has no branches, so
// an entry id has nothing to select.
func (s *Server) handleGetMessages(cmd proto.Command) proto.Response {
	var msgs []message.Message
	if store := s.sessionStore(); store == nil {
		msgs = s.history()
	} else {
		var err error
		if msgs, err = store.Messages(cmd.Entry); err != nil {
			return failResponse(cmd, proto.CodeBadRequest, err.Error())
		}
	}
	if msgs == nil {
		msgs = []message.Message{}
	}
	return okResponse(cmd, map[string]any{"messages": msgs})
}

// modelInfo is one catalog entry as the protocol carries it. The field names
// are the wire contract for list_models.
type modelInfo struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Provider        string   `json:"provider"`
	ContextWindow   int      `json:"context_window"`
	InPerM          float64  `json:"in_per_m"`
	OutPerM         float64  `json:"out_per_m"`
	InputModalities []string `json:"input_modalities,omitempty"`
	Current         bool     `json:"current"`
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
			ID:              m.ID,
			Name:            m.Name,
			Provider:        m.Provider,
			ContextWindow:   m.ContextWindow,
			InPerM:          m.InPerM,
			OutPerM:         m.OutPerM,
			InputModalities: m.InputModalities,
			Current:         m.ID == current,
		})
	}
	return okResponse(cmd, out)
}

// usageInfo is one OpenCode Go meter as the protocol carries it.
type usageInfo struct {
	Kind     string  `json:"kind"`
	Label    string  `json:"label"`
	Short    string  `json:"short"`
	Percent  float64 `json:"percent"`
	UsedUSD  float64 `json:"used_usd"`
	LimitUSD float64 `json:"limit_usd"`
	ResetsAt string  `json:"resets_at,omitempty"`
}

// usageTimeout bounds one OpenCode Go usage lookup, which a footer refresh
// must not let hang.
const usageTimeout = 5 * time.Second

// handleGetUsage fetches the OpenCode Go usage meters for the footer and the
// /usage view. A missing key is not an error: the response says so.
func (s *Server) handleGetUsage(ctx context.Context, cmd proto.Command) proto.Response {
	key, _, _ := providers.ResolveOpenCodeGoKey(s.cfg, os.Getenv)
	if key == "" {
		if stored, err := opencode.LoadKey(opencode.DefaultKeyPath(s.home)); err == nil {
			key = stored
		}
	}
	if key == "" {
		return okResponse(cmd, map[string]any{
			"meters": []usageInfo{}, "footer": "",
			"error": "Not signed in. Run `nova login opencode-go`.",
		})
	}
	ctx, cancel := context.WithTimeout(ctx, usageTimeout)
	defer cancel()
	meters, err := opencode.FetchUsage(ctx, opencode.Config{Server: os.Getenv("OPENCODE_CONSOLE_SERVER")}, key, "")
	if err != nil {
		return okResponse(cmd, map[string]any{
			"meters": []usageInfo{}, "footer": "", "error": opencode.DescribeUsageError(err),
		})
	}
	out := make([]usageInfo, 0, len(meters))
	rows := make([]string, 0, len(meters))
	now := time.Now()
	for _, m := range meters {
		out = append(out, usageInfo{
			Kind: string(m.Kind), Label: m.Label, Short: m.Short, Percent: m.Percent,
			UsedUSD: m.UsedUSD, LimitUSD: m.LimitUSD, ResetsAt: m.ResetsAt,
		})
		rows = append(rows, opencode.FormatMeterRow(m, 18, now))
	}
	return okResponse(cmd, map[string]any{"meters": out, "rows": rows, "footer": opencode.FooterStatus(meters, now)})
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
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return failResponse(cmd, proto.CodeInternal, "session.list: "+err.Error())
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

// handleGetExtensions returns the extension host's supervision status.
func (s *Server) handleGetExtensions(cmd proto.Command) proto.Response {
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
	for _, c := range s.ext.Commands() {
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
func (s *Server) handleRunCommand(ctx context.Context, cmd proto.Command) proto.Response {
	args := strings.TrimSpace(cmd.Args)
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
		if args == "" {
			s.mu.Lock()
			model := s.cfg.Model
			s.mu.Unlock()
			return okResponse(cmd, map[string]any{"text": model})
		}
		return s.handleSetModel(proto.Command{Type: proto.CmdSetModel, ID: cmd.ID, Model: args})
	case "thinking":
		if args == "" {
			s.mu.Lock()
			level := s.think
			s.mu.Unlock()
			return okResponse(cmd, map[string]any{"text": level})
		}
		return s.handleSetThinking(proto.Command{Type: proto.CmdSetThinking, ID: cmd.ID, Level: args})
	case "name":
		return s.handleSessionName(proto.Command{Type: proto.CmdSessionName, ID: cmd.ID, Name: cmd.Args})
	case "compact":
		return s.handleCompact(ctx, proto.Command{Type: proto.CmdCompact, ID: cmd.ID})
	case "new", "clear":
		// /new and /clear start a fresh session; both are the session.new
		// command, so the TUI, print, json, and rpc front doors all get it.
		return s.handleSessionNew(proto.Command{Type: proto.CmdSessionNew, ID: cmd.ID})
	case "reload":
		res, err := s.reload(ctx)
		if err != nil {
			return failResponse(cmd, proto.CodeBadRequest, err.Error())
		}
		return okResponse(cmd, map[string]any{"text": res.text()})
	case "quit", "exit":
		// Aliases for shutdown, so a headless client can quit the same way the
		// TUI's /quit does.
		return s.handleShutdown(cmd)
	}
	for _, tpl := range s.templates() {
		if tpl.Name == cmd.Name {
			text := tpl.Expand(cmd.Args)
			if strings.TrimSpace(text) == "" {
				return failResponse(cmd, proto.CodeBadRequest, "template produced an empty prompt")
			}
			if err := s.startRun(message.User(text)); err != nil {
				return startFailure(cmd, err)
			}
			return okResponse(cmd, nil)
		}
	}
	if s.hasExtensionCommand(cmd.Name) {
		out, err := s.ext.RunCommand(ctx, cmd.Name, cmd.Args)
		if err != nil {
			return failResponse(cmd, proto.CodeInternal, err.Error())
		}
		return okResponse(cmd, map[string]any{"text": out})
	}
	return failResponse(cmd, proto.CodeBadRequest, "unknown command "+cmd.Name)
}

// handleCompact runs compaction on demand.
func (s *Server) handleCompact(ctx context.Context, cmd proto.Command) proto.Response {
	store := s.sessionStore()
	if store == nil {
		return failResponse(cmd, proto.CodeBadRequest, "no session to compact")
	}
	out, err := compaction.Run(ctx, store, s.compactionState(), cmd.Instructions)
	if err != nil {
		if errors.Is(err, compaction.ErrNothingToCompact) {
			return failResponse(cmd, proto.CodeBadRequest, "nothing to compact")
		}
		return failResponse(cmd, proto.CodeInternal, "compaction failed: "+err.Error())
	}
	s.emitCompaction(out)
	return okResponse(cmd, nil)
}

// handleSessionName records a display name for the session.
func (s *Server) handleSessionName(cmd proto.Command) proto.Response {
	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		return failResponse(cmd, proto.CodeBadRequest, "session.name: name is required")
	}
	store := s.sessionStore()
	if store == nil {
		return failResponse(cmd, proto.CodeBadRequest, "no session")
	}
	if _, err := store.Append(session.Entry{Type: session.TypeSessionInfo, Name: name}); err != nil {
		return failResponse(cmd, proto.CodeInternal, err.Error())
	}
	s.emitSession()
	return okResponse(cmd, nil)
}

// handleSessionNavigate moves the active leaf.
func (s *Server) handleSessionNavigate(cmd proto.Command) proto.Response {
	store := s.sessionStore()
	if store == nil {
		return failResponse(cmd, proto.CodeBadRequest, "no session")
	}
	if s.agent.Running() {
		return busyResponse(cmd)
	}
	if err := store.SetLeaf(cmd.Entry); err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	s.forgetLastUsage()
	s.emitSession()
	return okResponse(cmd, map[string]any{"leaf": store.Leaf()})
}

// forgetLastUsage drops the last response's token counts. They describe the
// conversation that produced them, so once the active branch or session
// changes they would make a short conversation look as full as the old one.
func (s *Server) forgetLastUsage() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUse = provider.Usage{}
}

// handleSessionTree returns every entry as a nested tree.
func (s *Server) handleSessionTree(cmd proto.Command) proto.Response {
	store := s.sessionStore()
	if store == nil {
		return okResponse(cmd, map[string]any{"tree": []any{}})
	}
	return okResponse(cmd, map[string]any{"tree": entryTree(store, "")})
}

// treeNode is one entry and its children.
type treeNode struct {
	Entry    session.Entry `json:"entry"`
	Children []*treeNode   `json:"children,omitempty"`
}

// entryTree builds the entry tree below parent.
func entryTree(store *session.Store, parent string) []*treeNode {
	var out []*treeNode
	for _, id := range store.Children(parent) {
		entry, ok := store.Get(id)
		if !ok {
			continue
		}
		out = append(out, &treeNode{Entry: entry, Children: entryTree(store, id)})
	}
	return out
}

// emitSession emits a session_changed event.
func (s *Server) emitSession() {
	store := s.sessionStore()
	s.emit(proto.Event{Type: proto.EventSessionChanged, Session: storeID(store), Leaf: storeLeaf(store)})
}

// handleSessionNew starts a fresh session file.
func (s *Server) handleSessionNew(cmd proto.Command) proto.Response {
	if s.agent.Running() {
		return busyResponse(cmd)
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
		return busyResponse(cmd)
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
		return busyResponse(cmd)
	}
	store := s.sessionStore()
	if store == nil {
		return failResponse(cmd, proto.CodeBadRequest, "no session")
	}
	dst := defaultSessionPath(s.cwd)
	forked, err := session.Fork(store, cmd.Entry, dst, session.Options{})
	if err != nil {
		return failResponse(cmd, proto.CodeBadRequest, err.Error())
	}
	s.swapStore(forked, dst)
	s.emitSession()
	return okResponse(cmd, map[string]any{"id": s.sessionID(), "path": dst})
}

// openSession closes the current store and opens path.
func (s *Server) openSession(path string) error {
	store, err := session.Open(path, session.Options{})
	if err != nil {
		return err
	}
	s.swapStore(store, path)
	return nil
}

// swapStore installs a new store and closes the old one. The in-memory
// transcript and the last response's usage belonged to the old conversation, so
// they go with it.
func (s *Server) swapStore(store *session.Store, path string) {
	s.mu.Lock()
	old := s.store
	s.store, s.path, s.mem, s.lastUse = store, path, nil, provider.Usage{}
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
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
func (s *Server) handleBash(ctx context.Context, cmd proto.Command) proto.Response {
	command, quiet := strings.CutPrefix(strings.TrimSpace(cmd.Command), "!!")
	if command = strings.TrimSpace(command); command == "" {
		return failResponse(cmd, proto.CodeBadRequest, "bash: command is required")
	}
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		return failResponse(cmd, proto.CodeInternal, err.Error())
	}
	// The pool always holds bash, whether or not the model may call it: a user
	// shell command is the user's own, so it skips the permission policy.
	res, err := s.pool["bash"].Run(ctx, args, &tool.Context{WorkDir: s.cwd, Roots: []string{s.cwd}})
	if err != nil {
		res = tool.Errorf("%v", err)
	}
	if store := s.sessionStore(); !quiet && store != nil {
		entry := session.Entry{Type: session.TypeCustomMessage, Content: res.Content, Display: true, InContext: false}
		if _, err := store.Append(entry); err != nil {
			s.emit(proto.Event{Type: proto.EventError, Error: &proto.Error{Code: proto.CodeInternal, Message: "bash output was not recorded: " + err.Error()}})
		}
	}
	return okResponse(cmd, map[string]any{"content": res.Content, "is_error": res.IsError})
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
