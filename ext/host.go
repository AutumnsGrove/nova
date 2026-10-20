package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
	"github.com/AutumnsGrove/nova/usage"
)

// defaultCompleteMax caps nested model calls per extension per run.
const defaultCompleteMax = 20

// defaultToolCallTimeout bounds one protocol tool call.
const defaultToolCallTimeout = 120 * time.Second

// maxRegistrations caps the tools and the commands one extension may register.
// Every tool definition is sent to the model on each request, so an unbounded
// list would let an extension inflate every later request.
const maxRegistrations = 128

// SessionAPI is the piece of the session the host needs.
type SessionAPI interface {
	AppendEntry(kind string, data json.RawMessage, keep bool) error
	Entries(kind string) []json.RawMessage
}

// UIBridge forwards an extension's UI requests to the attached client.
type UIBridge interface {
	Request(ctx context.Context, req proto.UIRequest) (proto.UIResponse, error)
	Notify(req proto.UIRequest)
}

// Command is one extension slash command.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Extension   string `json:"extension"`
}

// Status is one extension's supervision state.
type Status struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	State     string `json:"state"`
	Restarts  int    `json:"restarts"`
	LastError string `json:"last_error,omitempty"`
}

// HostOptions configures an extension Host.
type HostOptions struct {
	Dirs        []string // user extensions first, then trusted project ones
	Disabled    []string
	Registry    *tool.Registry
	Session     SessionAPI
	SessionID   string
	UI          UIBridge
	Complete    func(ctx context.Context, req provider.Request) (*provider.Response, error)
	CompleteMax int
	Emit        func(proto.Event)
	NovaVersion string
	CWD         string
	Mode        string
	Env         func() []string
	ScrubEnv    []string
	PassEnv     []string
	Flags       map[string]string
	EntryKinds  map[string][]string
	Run         CommandRunner
	Clock       Clock
	Log         func(level, msg string)

	// Config, Ledger, Extra, Spawn, and HasUI are handed to bundled
	// extensions through Env. They may be nil.
	Config *config.Config
	Ledger *usage.Ledger
	Extra  map[string]any
	Spawn  func(ctx context.Context, r SpawnRequest) (*SpawnResult, error)
	HasUI  func(capability string) bool
}

// RegisterTool is a tool an extension declares in register.
type RegisterTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	ReadOnly    bool           `json:"read_only"`
	Destructive bool           `json:"destructive"`
	OpenWorld   bool           `json:"open_world"`
	Parallel    bool           `json:"parallel"`
	TimeoutMS   int            `json:"timeout_ms"`
}

// RegisterCommand is a slash command an extension declares in register.
type RegisterCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// registerFrame is the extension's register frame.
type registerFrame struct {
	Tools     []RegisterTool    `json:"tools"`
	Commands  []RegisterCommand `json:"commands"`
	Hooks     []string          `json:"hooks"`
	Flags     []FlagSpec        `json:"flags"`
	Providers []map[string]any  `json:"providers"`
	Entries   []string          `json:"entries"`
}

// Host supervises extension processes and routes their registrations into the
// tool registry and the agent's hooks.
type Host struct {
	opt   HostOptions
	clock Clock
	logf  func(level, msg string)
	reg   *tool.Registry

	// life serialises Start and Reload, which both rebuild the instance list.
	life sync.Mutex

	mu             sync.Mutex
	closed         bool // set by Shutdown; nothing may start afterwards
	insts          []*instance
	byName         map[string]*instance
	complete       map[string]int
	hookErrAt      map[string]time.Time
	entryKinds     map[string][]string
	manifestHashes map[string]string
	bundled        []Bundled
	started        bool
}

// NewHost builds a host. Nothing starts until Start.
func NewHost(opt HostOptions) *Host {
	clock := opt.Clock
	if clock == nil {
		clock = realClock{}
	}
	logf := opt.Log
	if logf == nil {
		logf = func(string, string) {}
	}
	reg := opt.Registry
	if reg == nil {
		reg = tool.NewRegistry()
	}
	return &Host{
		opt:            opt,
		clock:          clock,
		logf:           logf,
		reg:            reg,
		byName:         map[string]*instance{},
		complete:       map[string]int{},
		hookErrAt:      map[string]time.Time{},
		entryKinds:     map[string][]string{},
		manifestHashes: map[string]string{},
	}
}

// Registry returns the tool registry the host registers into.
func (h *Host) Registry() *tool.Registry { return h.reg }

// Start loads and starts every enabled extension. Errors are per extension.
// Bundled extensions start first, so their tool names are reserved before a
// third-party extension can claim them.
func (h *Host) Start(ctx context.Context) []error {
	h.life.Lock()
	defer h.life.Unlock()
	return h.start(ctx)
}

// start is Start without the lifecycle lock, for Reload, which already holds it.
func (h *Host) start(ctx context.Context) []error {
	var errs []error
	h.mu.Lock()
	bundled := slices.Clone(h.bundled)
	h.mu.Unlock()
	for _, b := range bundled {
		if err := h.startBundled(b); err != nil {
			errs = append(errs, err)
		}
	}
	for _, dir := range h.opt.Dirs {
		m, err := LoadManifest(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if h.isDisabled(m.Name) {
			continue
		}
		inst := newInstance(h, m)
		if !h.addInstance(inst) {
			break
		}
		if err := h.registerDeclarative(inst); err != nil {
			errs = append(errs, err)
			continue
		}
		h.mu.Lock()
		kinds := append(slices.Clone(h.opt.EntryKinds[m.Name]), h.entryKinds[m.Name]...)
		h.mu.Unlock()
		inst.mu.Lock()
		inst.entryKinds = kinds
		inst.mu.Unlock()
		if len(m.Run) == 0 {
			inst.setState(stateRunning, "")
			continue
		}
		if err := inst.start(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	h.mu.Lock()
	h.started = true
	h.mu.Unlock()
	h.recordManifestHashes()
	return errs
}

// addInstance records an instance in load order. It refuses once Shutdown has
// begun, so nothing can start behind Shutdown's back.
func (h *Host) addInstance(inst *instance) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.insts = append(h.insts, inst)
	h.byName[inst.manifest.Name] = inst
	return true
}

// isDisabled reports whether a name is in the disabled list.
func (h *Host) isDisabled(name string) bool {
	return slices.Contains(h.opt.Disabled, name)
}

// registerDeclarative registers a manifest's exec tools. A tool without exec
// is only a description of one the process registers itself.
func (h *Host) registerDeclarative(inst *instance) error {
	for _, spec := range inst.manifest.Tools {
		if len(spec.Exec) == 0 {
			continue
		}
		if err := h.registerTool(inst, &execTool{dir: inst.manifest.Dir, spec: spec}, true); err != nil {
			return err
		}
	}
	return nil
}

// registerTool adds one tool, refusing a name already taken, and records it as
// the instance's own: a declarative tool (decl) or a process tool.
func (h *Host) registerTool(inst *instance, t tool.Tool, decl bool) error {
	name := t.Info().Name
	if _, exists := h.reg.Get(name); exists {
		return fmt.Errorf("ext: tool %q is already registered", name)
	}
	if err := h.reg.Register(t); err != nil {
		return err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if decl {
		inst.reg.decl = append(inst.reg.decl, name)
	} else {
		inst.reg.tools = append(inst.reg.tools, name)
	}
	return nil
}

// handleFrame dispatches one unsolicited frame that arrived on c.
func (h *Host) handleFrame(inst *instance, c *conn, typ string, line []byte, r reply) {
	switch typ {
	case "register":
		h.handleRegister(inst, c, line)
	case "ui_request":
		h.handleUI(inst, c, line)
	case "append_entry":
		h.handleAppend(inst, c, line)
	case "complete":
		h.handleComplete(inst, c, line)
	case "log":
		var f struct {
			Level string `json:"level"`
			Text  string `json:"text"`
		}
		if json.Unmarshal(line, &f) == nil {
			h.logf(f.Level, inst.manifest.Name+": "+f.Text)
		}
	case "__log":
		h.logf(r.Type, inst.manifest.Name+": "+r.Content)
	case "hook_result", "tool_result", "error":
		h.logf("warn", inst.manifest.Name+": reply for unknown id "+r.ID)
	default:
		h.logf("warn", inst.manifest.Name+": unknown frame type "+typ)
	}
}

// handleRegister replaces the instance's registrations with the frame's.
func (h *Host) handleRegister(inst *instance, c *conn, line []byte) {
	var f registerFrame
	if err := json.Unmarshal(line, &f); err != nil {
		h.deny(inst, c, "", proto.CodeBadRequest, "malformed register frame")
		return
	}
	inst.mu.Lock()
	inst.entryKinds = slices.Clone(f.Entries)
	inst.reg.flags = slices.Clone(f.Flags)
	inst.mu.Unlock()
	h.mu.Lock()
	h.entryKinds[inst.manifest.Name] = slices.Clone(f.Entries)
	h.mu.Unlock()
	h.registerProcessTools(inst, c, f.Tools)
	h.registerCommandsAndHooks(inst, c, f)
	if len(f.Providers) > 0 && !inst.manifest.hasCapability("providers") {
		h.deny(inst, c, "", proto.CodePermissionDenied, "providers capability not declared")
	}
	inst.markRegistered()
}

// registerProcessTools makes the register frame's tools the instance's process
// tools. A tool the frame leaves out is dropped only after its replacements
// are in place, so a re-register never opens an empty window for readers of the
// registry.
func (h *Host) registerProcessTools(inst *instance, c *conn, defs []RegisterTool) {
	if len(defs) > maxRegistrations {
		h.deny(inst, c, "", proto.CodeBadRequest, fmt.Sprintf("an extension may register at most %d tools", maxRegistrations))
		defs = defs[:maxRegistrations]
	}
	if len(defs) > 0 && !inst.manifest.hasCapability("tools") {
		h.deny(inst, c, "", proto.CodePermissionDenied, "tools capability not declared")
		defs = nil
	}
	inst.mu.Lock()
	old := slices.Clone(inst.reg.tools)
	inst.mu.Unlock()
	kept := map[string]bool{}
	for _, def := range defs {
		if err := validateRegisterTool(def); err != nil {
			h.deny(inst, c, "", proto.CodeBadRequest, err.Error())
			continue
		}
		if err := h.registerOrReplace(inst, &protocolTool{host: h, inst: inst, def: def}); err != nil {
			h.deny(inst, c, "", proto.CodeBadRequest, err.Error())
			continue
		}
		kept[def.Name] = true
	}
	for _, name := range old {
		if !kept[name] {
			h.reg.Unregister(name)
			inst.forgetTool(name)
		}
	}
}

// validateRegisterTool applies to a process tool the rules the manifest applies
// to a declarative one, because a malformed name or schema would fail every
// later model request, not just calls to the tool.
func validateRegisterTool(def RegisterTool) error {
	if !toolNameRe.MatchString(def.Name) {
		return fmt.Errorf("ext: invalid tool name %q", def.Name)
	}
	if def.Parameters != nil && def.Parameters["type"] != "object" {
		return fmt.Errorf("ext: tool %q: parameters must be an object schema", def.Name)
	}
	return nil
}

// forgetTool drops a name from the instance's process tools.
func (inst *instance) forgetTool(name string) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.reg.tools = slices.DeleteFunc(inst.reg.tools, func(n string) bool { return n == name })
}

// registerOrReplace registers a tool, replacing one this instance already owns.
func (h *Host) registerOrReplace(inst *instance, t tool.Tool) error {
	name := t.Info().Name
	inst.mu.Lock()
	owned := slices.Contains(inst.reg.tools, name)
	inst.mu.Unlock()
	if owned {
		return h.reg.Replace(t)
	}
	return h.registerTool(inst, t, false)
}

// registerCommandsAndHooks records the frame's commands and hooks, refusing
// those the manifest did not declare a capability for.
func (h *Host) registerCommandsAndHooks(inst *instance, c *conn, f registerFrame) {
	var commands []RegisterCommand
	switch {
	case len(f.Commands) == 0:
	case !inst.manifest.hasCapability("commands"):
		h.deny(inst, c, "", proto.CodePermissionDenied, "commands capability not declared")
	default:
		commands = slices.Clone(f.Commands[:min(len(f.Commands), maxRegistrations)])
	}
	var hooks []string
	for _, hook := range f.Hooks {
		switch {
		case !hookNames[hook]:
			h.deny(inst, c, "", proto.CodeBadRequest, "unknown hook "+hook)
		case !inst.manifest.hasCapability("hooks:" + hook):
			h.deny(inst, c, "", proto.CodePermissionDenied, "hook "+hook+" capability not declared")
		default:
			hooks = append(hooks, hook)
		}
	}
	inst.mu.Lock()
	inst.reg.command = commands
	inst.reg.hooks = hooks
	inst.mu.Unlock()
}

// clearProcessRegistrations removes what an instance's process contributed:
// its tools, commands, and hooks. Declarative tools stay, since they do not
// depend on the process.
func (h *Host) clearProcessRegistrations(inst *instance) {
	inst.mu.Lock()
	tools := inst.reg.tools
	inst.reg.tools = nil
	inst.reg.command = nil
	inst.reg.hooks = nil
	inst.mu.Unlock()
	for _, name := range tools {
		h.reg.Unregister(name)
	}
}

// clearRegistrations removes everything an instance registered.
func (h *Host) clearRegistrations(inst *instance) {
	h.clearProcessRegistrations(inst)
	inst.mu.Lock()
	decl := inst.reg.decl
	inst.reg.decl = nil
	inst.mu.Unlock()
	for _, name := range decl {
		h.reg.Unregister(name)
	}
}

// deny tells an extension a frame was refused and logs why, so `nova ext check`
// can report it. id is the refused request's id, when it had one, so the
// extension's waiting call fails instead of hanging.
func (h *Host) deny(inst *instance, c *conn, id, code, msg string) {
	h.logf("warn", inst.manifest.Name+": "+msg)
	_ = c.notify(&outFrame{Type: "error", ID: id, Error: &proto.Error{Code: code, Message: msg}})
}

// handleUI forwards a UI request, checking the method capability. A request
// that needs an answer waits on its own goroutine, so a user at a prompt does
// not stop the extension's other frames from being read.
func (h *Host) handleUI(inst *instance, c *conn, line []byte) {
	var req proto.UIRequest
	if err := json.Unmarshal(line, &req); err != nil {
		h.deny(inst, c, "", proto.CodeBadRequest, "malformed ui_request")
		return
	}
	if !uiMethods[req.Method] || !inst.manifest.hasCapability("ui:"+req.Method) {
		h.deny(inst, c, req.ID, proto.CodePermissionDenied, "ui method "+req.Method+" is not declared")
		return
	}
	if h.opt.UI == nil {
		h.deny(inst, c, req.ID, proto.CodeUnsupported, "no UI client is attached")
		return
	}
	if fireAndForget(req.Method) {
		h.opt.UI.Notify(req)
		return
	}
	if !c.async(func(ctx context.Context) {
		resp, err := h.opt.UI.Request(ctx, req)
		if err != nil {
			h.deny(inst, c, req.ID, proto.CodeUnsupported, err.Error())
			return
		}
		resp.Type = proto.FrameUIResponse
		resp.ID = req.ID
		_ = c.notify(resp)
	}) {
		h.deny(inst, c, req.ID, proto.CodeBusy, "too many requests in flight")
	}
}

// fireAndForget reports whether a UI method needs no answer.
func fireAndForget(method string) bool {
	switch method {
	case "notify", "status", "widget", "title", "set_editor_text":
		return true
	}
	return false
}

// handleAppend stores one custom session entry.
func (h *Host) handleAppend(inst *instance, c *conn, line []byte) {
	if !inst.manifest.hasCapability("session:append_entry") {
		h.deny(inst, c, "", proto.CodePermissionDenied, "session:append_entry capability not declared")
		return
	}
	if h.opt.Session == nil {
		h.deny(inst, c, "", proto.CodeInternal, "no session is attached")
		return
	}
	var f struct {
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
		Keep bool            `json:"keep"`
	}
	if err := json.Unmarshal(line, &f); err != nil || f.Kind == "" {
		h.deny(inst, c, "", proto.CodeBadRequest, "append_entry needs kind and data")
		return
	}
	if err := h.opt.Session.AppendEntry(f.Kind, f.Data, f.Keep); err != nil {
		h.deny(inst, c, "", proto.CodeInternal, err.Error())
	}
}

// handleComplete performs a nested model call under the per-run cap, on its own
// goroutine so a slow model does not stop the extension's other frames from
// being read.
func (h *Host) handleComplete(inst *instance, c *conn, line []byte) {
	if !inst.manifest.hasCapability("model:complete") {
		h.deny(inst, c, "", proto.CodePermissionDenied, "model:complete capability not declared")
		return
	}
	var f struct {
		ID      string           `json:"id"`
		Request provider.Request `json:"request"`
	}
	if err := json.Unmarshal(line, &f); err != nil {
		h.deny(inst, c, "", proto.CodeBadRequest, "malformed complete frame")
		return
	}
	if h.opt.Complete == nil {
		h.deny(inst, c, f.ID, proto.CodeUnsupported, "no completion function is attached")
		return
	}
	limit := h.opt.CompleteMax
	if limit <= 0 {
		limit = defaultCompleteMax
	}
	h.mu.Lock()
	h.complete[inst.manifest.Name]++
	over := h.complete[inst.manifest.Name] > limit
	h.mu.Unlock()
	if over {
		h.deny(inst, c, f.ID, proto.CodeBadRequest, fmt.Sprintf("complete cap of %d reached for this run", limit))
		return
	}
	if !c.async(func(ctx context.Context) {
		resp, err := h.opt.Complete(ctx, f.Request)
		if err != nil {
			h.deny(inst, c, f.ID, proto.CodeProviderError, err.Error())
			return
		}
		frame := &outFrame{Type: "complete_result", ID: f.ID}
		if resp != nil {
			frame.Message = resp.Message
			frame.Usage = resp.Usage
		}
		_ = c.notify(frame)
	}) {
		h.deny(inst, c, f.ID, proto.CodeBusy, "too many requests in flight")
	}
}

// Commands returns every running extension's slash commands in load order.
func (h *Host) Commands() []Command {
	var out []Command
	for _, inst := range h.snapshot() {
		inst.mu.Lock()
		state := inst.state
		cmds := slices.Clone(inst.reg.command)
		inst.mu.Unlock()
		if state != stateRunning {
			continue
		}
		for _, c := range cmds {
			out = append(out, Command{Name: c.Name, Description: c.Description, Extension: inst.manifest.Name})
		}
	}
	return out
}

// snapshot returns the instances in load order.
func (h *Host) snapshot() []*instance {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.insts)
}

// commandOwner returns the first running extension, in load order, that
// registered a slash command called name.
func (h *Host) commandOwner(name string) *instance {
	for _, inst := range h.snapshot() {
		inst.mu.Lock()
		running := inst.state == stateRunning
		owns := slices.ContainsFunc(inst.reg.command, func(c RegisterCommand) bool { return c.Name == name })
		inst.mu.Unlock()
		if running && owns {
			return inst
		}
	}
	return nil
}

// RunCommand runs one extension command and returns its output.
func (h *Host) RunCommand(ctx context.Context, name, args string) (string, error) {
	target := h.commandOwner(name)
	if target == nil {
		return "", fmt.Errorf("ext: no extension command named %q", name)
	}
	target.mu.Lock()
	bundled := target.bundled
	cmd, hasCmd := target.cmdRun[name]
	target.mu.Unlock()
	if bundled != nil {
		if !hasCmd {
			return "", fmt.Errorf("ext: no extension command named %q", name)
		}
		return h.runBundledCommand(ctx, target, cmd, args)
	}
	reply, err := target.request(ctx, &outFrame{Type: "command", Name: name, Args: args}, defaultToolCallTimeout)
	if err != nil {
		return "", fmt.Errorf("ext: %s: %w", target.manifest.Name, err)
	}
	if reply.Error != nil {
		return "", fmt.Errorf("ext: %s: %s", target.manifest.Name, reply.Error.Message)
	}
	if reply.IsError {
		return "", fmt.Errorf("ext: %s: %s", target.manifest.Name, reply.Content)
	}
	return reply.Content, nil
}

// Status returns the supervision state of every instance, sorted by name.
func (h *Host) Status() []Status {
	insts := h.snapshot()
	out := make([]Status, 0, len(insts))
	for _, inst := range insts {
		out = append(out, inst.status())
	}
	slices.SortStableFunc(out, func(a, b Status) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Shutdown stops every extension in parallel, so one that ignores the request
// does not delay the others by its grace period. No process survives it, and
// nothing can start once it has begun. It reports ctx's error if ctx ended
// before the supervisors finished.
func (h *Host) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	insts := slices.Clone(h.insts)
	h.mu.Unlock()
	var wg sync.WaitGroup
	for _, inst := range insts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inst.stop(ctx)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

// disable marks an instance disabled, removes its registrations, and emits one
// extension_error.
func (h *Host) disable(inst *instance, reason string) {
	h.clearRegistrations(inst)
	inst.setState(stateDisabled, reason)
	if c := inst.getConn(); c != nil {
		c.tryNotify(&outFrame{Type: "shutdown"})
		c.close()
	}
	if h.opt.Emit != nil {
		h.opt.Emit(proto.Event{
			Type:      proto.EventExtensionError,
			Extension: inst.manifest.Name,
			Error:     &proto.Error{Code: proto.CodeInternal, Message: reason},
		})
	}
}

// emitError is the shared extension_error helper for hooks.
func (h *Host) emitError(inst *instance, hook, msg string) {
	if h.opt.Emit == nil {
		return
	}
	h.opt.Emit(proto.Event{
		Type:      proto.EventExtensionError,
		Extension: inst.manifest.Name,
		Hook:      hook,
		Error:     &proto.Error{Code: proto.CodeInternal, Message: msg},
	})
}

// realClock is the wall-clock Clock used when none is injected.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
