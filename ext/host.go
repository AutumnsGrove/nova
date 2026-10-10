package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// defaultCompleteMax caps nested model calls per extension per run.
const defaultCompleteMax = 20

// defaultToolCallTimeout bounds one protocol tool call.
const defaultToolCallTimeout = 120 * time.Second

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
	Clock       Clock
	Log         func(level, msg string)
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

	mu        sync.Mutex
	insts     []*instance
	byName    map[string]*instance
	complete  map[string]int
	hookErrAt map[string]time.Time
	started   bool
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
		opt:       opt,
		clock:     clock,
		logf:      logf,
		reg:       reg,
		byName:    map[string]*instance{},
		complete:  map[string]int{},
		hookErrAt: map[string]time.Time{},
	}
}

// Registry returns the tool registry the host registers into.
func (h *Host) Registry() *tool.Registry { return h.reg }

// Start loads and starts every enabled extension. Errors are per extension.
func (h *Host) Start(ctx context.Context) []error {
	var errs []error
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
		h.addInstance(inst)
		if err := h.registerDeclarative(inst); err != nil {
			errs = append(errs, err)
			continue
		}
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
	return errs
}

// addInstance records an instance in load order.
func (h *Host) addInstance(inst *instance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.insts = append(h.insts, inst)
	h.byName[inst.manifest.Name] = inst
}

// isDisabled reports whether a name is in the disabled list.
func (h *Host) isDisabled(name string) bool {
	for _, d := range h.opt.Disabled {
		if d == name {
			return true
		}
	}
	return false
}

// registerDeclarative registers a manifest's declarative tools.
func (h *Host) registerDeclarative(inst *instance) error {
	for _, spec := range inst.manifest.Tools {
		t := &execTool{dir: inst.manifest.Dir, spec: spec}
		if err := h.registerTool(inst, t.Info(), t); err != nil {
			return err
		}
	}
	return nil
}

// registerTool adds one tool, refusing a name already taken.
func (h *Host) registerTool(inst *instance, info tool.Info, t tool.Tool) error {
	if _, exists := h.reg.Get(info.Name); exists {
		return fmt.Errorf("ext: tool %q is already registered", info.Name)
	}
	if err := h.reg.Register(t); err != nil {
		return err
	}
	inst.reg.tools = append(inst.reg.tools, info.Name)
	return nil
}

// handleFrame dispatches one unsolicited frame from an instance.
func (h *Host) handleFrame(inst *instance, typ string, line []byte, r reply) {
	switch typ {
	case "register":
		h.handleRegister(inst, line)
	case "ui_request":
		h.handleUI(inst, line)
	case "append_entry":
		h.handleAppend(inst, line)
	case "complete":
		h.handleComplete(inst, line)
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
func (h *Host) handleRegister(inst *instance, line []byte) {
	var f registerFrame
	if err := json.Unmarshal(line, &f); err != nil {
		h.deny(inst, proto.CodeBadRequest, "malformed register frame")
		return
	}
	h.clearRegistrations(inst)
	inst.entryKinds = append([]string(nil), f.Entries...)
	inst.reg.flags = append([]FlagSpec(nil), f.Flags...)
	for _, rt := range f.Tools {
		if !inst.manifest.hasCapability("tools") {
			h.deny(inst, proto.CodePermissionDenied, "tools capability not declared")
			continue
		}
		t := &protocolTool{host: h, inst: inst, def: rt}
		if err := h.registerTool(inst, t.Info(), t); err != nil {
			h.deny(inst, proto.CodeBadRequest, err.Error())
		}
	}
	if len(f.Commands) > 0 && !inst.manifest.hasCapability("commands") {
		h.deny(inst, proto.CodePermissionDenied, "commands capability not declared")
	} else {
		inst.reg.command = append([]RegisterCommand(nil), f.Commands...)
	}
	for _, hook := range f.Hooks {
		if !hookNames[hook] {
			h.deny(inst, proto.CodeBadRequest, "unknown hook "+hook)
			continue
		}
		if !inst.manifest.hasCapability("hooks:" + hook) {
			h.deny(inst, proto.CodePermissionDenied, "hook "+hook+" capability not declared")
			continue
		}
		inst.reg.hooks = append(inst.reg.hooks, hook)
	}
	if len(f.Providers) > 0 && !inst.manifest.hasCapability("providers") {
		h.deny(inst, proto.CodePermissionDenied, "providers capability not declared")
	}
	inst.markRegistered()
}

// clearRegistrations removes an instance's tools and command/hook lists.
func (h *Host) clearRegistrations(inst *instance) {
	if inst == nil {
		return
	}
	for _, name := range inst.reg.tools {
		h.reg.Unregister(name)
	}
	inst.reg.tools = nil
	inst.reg.command = nil
	inst.reg.hooks = nil
}

// deny sends an error frame to an extension.
func (h *Host) deny(inst *instance, code, msg string) {
	if inst.conn == nil {
		return
	}
	_ = inst.conn.notify(&outFrame{Type: "error", Error: &proto.Error{Code: code, Message: msg}})
}

// handleUI forwards a UI request, checking the method capability.
func (h *Host) handleUI(inst *instance, line []byte) {
	var req proto.UIRequest
	if err := json.Unmarshal(line, &req); err != nil {
		h.deny(inst, proto.CodeBadRequest, "malformed ui_request")
		return
	}
	if !uiMethods[req.Method] || !inst.manifest.hasCapability("ui:"+req.Method) {
		h.deny(inst, proto.CodePermissionDenied, "ui method "+req.Method+" is not declared")
		return
	}
	if h.opt.UI == nil {
		h.deny(inst, proto.CodeUnsupported, "no UI client is attached")
		return
	}
	if fireAndForget(req.Method) {
		h.opt.UI.Notify(req)
		return
	}
	resp, err := h.opt.UI.Request(context.Background(), req)
	if err != nil {
		h.deny(inst, proto.CodeUnsupported, err.Error())
		return
	}
	resp.Type = proto.FrameUIResponse
	resp.ID = req.ID
	_ = inst.conn.notify(resp)
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
func (h *Host) handleAppend(inst *instance, line []byte) {
	if !inst.manifest.hasCapability("session:append_entry") {
		h.deny(inst, proto.CodePermissionDenied, "session:append_entry capability not declared")
		return
	}
	if h.opt.Session == nil {
		h.deny(inst, proto.CodeInternal, "no session is attached")
		return
	}
	var f struct {
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
		Keep bool            `json:"keep"`
	}
	if err := json.Unmarshal(line, &f); err != nil || f.Kind == "" {
		h.deny(inst, proto.CodeBadRequest, "append_entry needs kind and data")
		return
	}
	if err := h.opt.Session.AppendEntry(f.Kind, f.Data, f.Keep); err != nil {
		h.deny(inst, proto.CodeInternal, err.Error())
	}
}

// handleComplete performs a nested model call under the per-run cap.
func (h *Host) handleComplete(inst *instance, line []byte) {
	if !inst.manifest.hasCapability("model:complete") {
		h.deny(inst, proto.CodePermissionDenied, "model:complete capability not declared")
		return
	}
	var f struct {
		ID      string           `json:"id"`
		Request provider.Request `json:"request"`
	}
	if err := json.Unmarshal(line, &f); err != nil {
		h.deny(inst, proto.CodeBadRequest, "malformed complete frame")
		return
	}
	if h.opt.Complete == nil {
		h.deny(inst, proto.CodeUnsupported, "no completion function is attached")
		return
	}
	h.mu.Lock()
	max := h.opt.CompleteMax
	if max <= 0 {
		max = defaultCompleteMax
	}
	h.complete[inst.manifest.Name]++
	over := h.complete[inst.manifest.Name] > max
	h.mu.Unlock()
	if over {
		h.deny(inst, proto.CodeBadRequest, fmt.Sprintf("complete cap of %d reached for this run", max))
		return
	}
	resp, err := h.opt.Complete(context.Background(), f.Request)
	if err != nil {
		h.deny(inst, proto.CodeProviderError, err.Error())
		return
	}
	frame := &outFrame{Type: "complete_result", ID: f.ID}
	if resp != nil {
		frame.Message = resp.Message
		frame.Usage = resp.Usage
	}
	_ = inst.conn.notify(frame)
}

// Commands returns every running extension's slash commands in load order.
func (h *Host) Commands() []Command {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Command
	for _, inst := range h.insts {
		inst.mu.Lock()
		state := inst.state
		cmds := append([]RegisterCommand(nil), inst.reg.command...)
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

// RunCommand runs one extension command and returns its output.
func (h *Host) RunCommand(ctx context.Context, name, args string) (string, error) {
	h.mu.Lock()
	var target *instance
	for _, inst := range h.insts {
		inst.mu.Lock()
		state := inst.state
		inst.mu.Unlock()
		if state != stateRunning {
			continue
		}
		for _, c := range inst.reg.command {
			if c.Name == name {
				target = inst
			}
		}
	}
	h.mu.Unlock()
	if target == nil {
		return "", fmt.Errorf("ext: no extension command named %q", name)
	}
	reply, err := target.conn.request(ctx, &outFrame{Type: "command", Name: name, Args: args}, defaultToolCallTimeout)
	if err != nil {
		return "", fmt.Errorf("ext: %s: %w", target.manifest.Name, err)
	}
	if reply.Error != nil {
		return "", fmt.Errorf("ext: %s: %s", target.manifest.Name, reply.Error.Message)
	}
	return reply.Content, nil
}

// Status returns the supervision state of every instance in load order.
func (h *Host) Status() []Status {
	h.mu.Lock()
	insts := append([]*instance(nil), h.insts...)
	h.mu.Unlock()
	out := make([]Status, 0, len(insts))
	for _, inst := range insts {
		out = append(out, inst.status())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Shutdown stops every extension. No process survives it.
func (h *Host) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	insts := append([]*instance(nil), h.insts...)
	h.mu.Unlock()
	var errs []error
	for _, inst := range insts {
		inst.stop(ctx)
	}
	return errors.Join(errs...)
}

// disable marks an instance disabled, removes its registrations, and emits one
// extension_error.
func (h *Host) disable(inst *instance, reason string) {
	h.clearRegistrations(inst)
	inst.setState(stateDisabled, reason)
	if inst.conn != nil {
		_ = inst.conn.notify(&outFrame{Type: "shutdown"})
		inst.conn.closeWith(errExited)
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
