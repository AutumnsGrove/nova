package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
	"github.com/AutumnsGrove/nova/usage"
)

// eventCallbackBudget is how long one Events observer may take for one event
// before it is skipped. The loop is never blocked by a slow observer.
const eventCallbackBudget = 100 * time.Millisecond

// eventObserver is a bundled extension's event callback.
type eventObserver func(proto.Event)

// Bundled is an in-tree extension that runs inside the nova process. It
// registers through the same host path as a subprocess extension, so a bundled
// extension is the reference for what a third-party one can do.
type Bundled interface {
	// Name is the extension name; it must match config's disable list.
	Name() string
	// Init registers the extension's tools, commands, hooks, and observers.
	// It is called once per start and again after every reload.
	Init(env *Env) error
}

// Env is what a bundled extension is handed at Init. Every function is safe to
// call; the registration functions may only be called from Init.
type Env struct {
	CWD    string
	Mode   string
	Config *config.Config
	Log    func(level, msg string)
	// Session stores custom entries and replays them on resume.
	Session SessionAPI
	// UI forwards UI requests to the attached client. It may be nil.
	UI UIBridge
	// Ledger is the session's usage ledger, so a nested call can be billed.
	Ledger *usage.Ledger
	// Extra carries host-specific values. "tui" is the TUI registry when the
	// TUI is present.
	Extra map[string]any

	// Tool registers a model-callable tool.
	Tool func(t tool.Tool) error
	// Command registers a slash command.
	Command func(c InProcCommand) error
	// Hooks contributes loop hooks, composed with every other extension's.
	Hooks func(h agent.Hooks)
	// Events observes every protocol event.
	Events func(fn func(proto.Event))
	// Complete performs one nested model call.
	Complete func(ctx context.Context, req provider.Request) (*provider.Response, error)
	// Spawn runs a fresh agent loop as a sub-agent.
	Spawn func(ctx context.Context, r SpawnRequest) (*SpawnResult, error)
	// HasUI reports whether the attached client declared a capability.
	HasUI func(capability string) bool
}

// InProcCommand is one bundled slack command.
type InProcCommand struct {
	Name        string
	Description string
	Run         func(ctx context.Context, args string) (string, error)
}

// SpawnRequest describes a child agent run. The caller's Depth is the parent's
// depth; the child runs at Depth+1.
type SpawnRequest struct {
	System   string
	Task     string
	Tools    []string
	Model    string
	MaxTurns int
	Depth    int
	OnEvent  func(proto.Event)
}

// SpawnResult is the outcome of one child run.
type SpawnResult struct {
	Answer string
	Turns  int
	Usage  provider.Usage
	Reason string
	Tools  []string
}

// AddBundled registers a bundled extension. It starts immediately when the
// host has already started, otherwise on the next Start.
func (h *Host) AddBundled(b Bundled) error {
	if b == nil || b.Name() == "" {
		return fmt.Errorf("ext: a bundled extension needs a name")
	}
	h.mu.Lock()
	if _, exists := h.byName[b.Name()]; exists {
		h.mu.Unlock()
		return fmt.Errorf("ext: extension %q is already registered", b.Name())
	}
	h.bundled = append(h.bundled, b)
	started := h.started
	h.mu.Unlock()
	if started {
		return h.startBundled(b)
	}
	return nil
}

// startBundled creates an instance for one bundled extension and calls Init.
func (h *Host) startBundled(b Bundled) error {
	if h.isDisabled(b.Name()) {
		return nil
	}
	h.mu.Lock()
	_, exists := h.byName[b.Name()]
	h.mu.Unlock()
	if exists {
		return nil
	}
	inst := newInstance(h, &Manifest{Name: b.Name(), Version: "bundled"})
	inst.bundled = b
	inst.cmdRun = map[string]InProcCommand{}
	inst.envHooks = agent.Hooks{}
	inst.bundleHooks = map[string]bool{}
	h.addInstance(inst)
	inst.env = h.newEnv(inst)
	return h.initBundle(inst)
}

// initBundle calls Init with panic recovery. A panic or an error disables just
// this extension, reports an extension_error, and leaves the rest running.
func (h *Host) initBundle(inst *instance) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("ext: %s panicked in Init: %v", inst.manifest.Name, rec)
			h.disable(inst, err.Error())
		}
	}()
	if initErr := inst.bundled.Init(inst.env); initErr != nil {
		err = fmt.Errorf("ext: %s: %w", inst.manifest.Name, initErr)
		h.disable(inst, err.Error())
		return err
	}
	inst.setState(stateRunning, "")
	inst.markRegistered()
	return nil
}

// newEnv builds the in-process API for one bundled instance.
func (h *Host) newEnv(inst *instance) *Env {
	name := inst.manifest.Name
	env := &Env{
		CWD:      h.opt.CWD,
		Mode:     h.opt.Mode,
		Config:   h.opt.Config,
		Log:      func(level, msg string) { h.logf(level, name+": "+msg) },
		Session:  h.opt.Session,
		UI:       h.opt.UI,
		Ledger:   h.opt.Ledger,
		Extra:    h.opt.Extra,
		Complete: h.opt.Complete,
		Spawn:    h.opt.Spawn,
		HasUI:    h.hasUI,
	}
	env.Tool = func(t tool.Tool) error {
		if _, exists := h.reg.Get(t.Info().Name); exists {
			return fmt.Errorf("ext: tool %q is already registered", t.Info().Name)
		}
		return h.registerTool(inst, t.Info(), &bundledTool{inst: inst, t: t})
	}
	env.Command = func(c InProcCommand) error {
		if c.Name == "" {
			return fmt.Errorf("ext: command needs a name")
		}
		inst.mu.Lock()
		defer inst.mu.Unlock()
		for _, existing := range inst.reg.command {
			if existing.Name == c.Name {
				return fmt.Errorf("ext: command %q is already registered", c.Name)
			}
		}
		inst.reg.command = append(inst.reg.command, RegisterCommand{Name: c.Name, Description: c.Description})
		inst.cmdRun[c.Name] = c
		return nil
	}
	env.Hooks = func(hk agent.Hooks) {
		inst.mu.Lock()
		mergeHooks(&inst.envHooks, hk)
		if hk.ToolCall != nil {
			inst.bundleHooks["tool_call"] = true
		}
		if hk.ToolResult != nil {
			inst.bundleHooks["tool_result"] = true
		}
		if hk.TransformContext != nil {
			inst.bundleHooks["transform_context"] = true
		}
		if hk.BeforeProviderRequest != nil {
			inst.bundleHooks["before_provider_request"] = true
		}
		if hk.MessageEnd != nil {
			inst.bundleHooks["message_end"] = true
		}
		if hk.TurnEnd != nil {
			inst.bundleHooks["turn_end"] = true
		}
		if hk.AgentEnd != nil {
			inst.bundleHooks["agent_end"] = true
		}
		inst.mu.Unlock()
	}
	env.Events = func(fn func(proto.Event)) {
		if fn == nil {
			return
		}
		inst.mu.Lock()
		inst.events = append(inst.events, fn)
		inst.mu.Unlock()
	}
	return env
}

// mergeHooks fills any unset hook in dst from src, so two calls to Env.Hooks
// compose rather than replace.
func mergeHooks(dst *agent.Hooks, src agent.Hooks) {
	if src.BeforeProviderRequest != nil {
		prev := dst.BeforeProviderRequest
		dst.BeforeProviderRequest = chainBeforeProviderRequest(prev, src.BeforeProviderRequest)
	}
	if src.TransformContext != nil {
		prev := dst.TransformContext
		dst.TransformContext = chainTransformContext(prev, src.TransformContext)
	}
	if src.MessageEnd != nil {
		prev := dst.MessageEnd
		dst.MessageEnd = chainMessageEnd(prev, src.MessageEnd)
	}
	if src.ToolCall != nil {
		prev := dst.ToolCall
		dst.ToolCall = chainToolCall(prev, src.ToolCall)
	}
	if src.ToolResult != nil {
		prev := dst.ToolResult
		dst.ToolResult = chainToolResult(prev, src.ToolResult)
	}
	if src.TurnEnd != nil {
		prev := dst.TurnEnd
		dst.TurnEnd = chainTurnEnd(prev, src.TurnEnd)
	}
	if src.AgentEnd != nil {
		prev := dst.AgentEnd
		dst.AgentEnd = chainAgentEnd(prev, src.AgentEnd)
	}
}

func chainBeforeProviderRequest(a, b func(context.Context, *provider.Request)) func(context.Context, *provider.Request) {
	if a == nil {
		return b
	}
	return func(ctx context.Context, req *provider.Request) { a(ctx, req); b(ctx, req) }
}

func chainTransformContext(a, b func(context.Context, []message.Message) []message.Message) func(context.Context, []message.Message) []message.Message {
	if a == nil {
		return b
	}
	return func(ctx context.Context, m []message.Message) []message.Message { return b(ctx, a(ctx, m)) }
}

func chainMessageEnd(a, b func(context.Context, message.Message) message.Message) func(context.Context, message.Message) message.Message {
	if a == nil {
		return b
	}
	return func(ctx context.Context, m message.Message) message.Message { return b(ctx, a(ctx, m)) }
}

func chainToolCall(a, b func(context.Context, agent.ToolCall) *agent.CallDecision) func(context.Context, agent.ToolCall) *agent.CallDecision {
	if a == nil {
		return b
	}
	return func(ctx context.Context, c agent.ToolCall) *agent.CallDecision {
		if dec := a(ctx, c); dec != nil {
			if dec.Block {
				return dec
			}
			if dec.Arguments != nil {
				c.Arguments = dec.Arguments
			}
		}
		return b(ctx, c)
	}
}

func chainToolResult(a, b func(context.Context, agent.ToolCall, tool.Result) tool.Result) func(context.Context, agent.ToolCall, tool.Result) tool.Result {
	if a == nil {
		return b
	}
	return func(ctx context.Context, c agent.ToolCall, r tool.Result) tool.Result { return b(ctx, c, a(ctx, c, r)) }
}

func chainTurnEnd(a, b func(context.Context, agent.Turn) agent.TurnDecision) func(context.Context, agent.Turn) agent.TurnDecision {
	if a == nil {
		return b
	}
	return func(ctx context.Context, t agent.Turn) agent.TurnDecision {
		first := a(ctx, t)
		second := b(ctx, t)
		out := first
		out.End = first.End || second.End
		out.Continue = first.Continue || second.Continue
		out.Inject = append(append([]message.Message(nil), first.Inject...), second.Inject...)
		return out
	}
}

func chainAgentEnd(a, b func(context.Context, *agent.Result)) func(context.Context, *agent.Result) {
	if a == nil {
		return b
	}
	return func(ctx context.Context, r *agent.Result) { a(ctx, r); b(ctx, r) }
}

// hasUI normalizes a capability name to the ui. prefix the hello uses.
func (h *Host) hasUI(capability string) bool {
	if h.opt.HasUI == nil {
		return false
	}
	if !strings.HasPrefix(capability, "ui.") {
		capability = "ui." + capability
	}
	return h.opt.HasUI(capability)
}

// hasUI is the Env-facing view.

// bundledTool adapts an in-process tool and recovers a panic into an error
// result, reporting it as an extension_error.
type bundledTool struct {
	inst *instance
	t    tool.Tool
}

func (b *bundledTool) Info() tool.Info { return b.t.Info() }

func (b *bundledTool) Run(ctx context.Context, args json.RawMessage, tctx *tool.Context) (res tool.Result, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			b.inst.host.reportHookErr(b.inst, "tool:"+b.t.Info().Name, fmt.Errorf("panic: %v", rec))
			res = tool.Errorf("extension %s: internal error in %s", b.inst.manifest.Name, b.t.Info().Name)
			err = nil
		}
	}()
	return b.t.Run(ctx, args, tctx)
}

// runBundledCommand runs a bundled command with panic recovery.
func (h *Host) runBundledCommand(inst *instance, cmd InProcCommand, ctx context.Context, args string) (out string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			h.reportHookErr(inst, "command:"+cmd.Name, fmt.Errorf("panic: %v", rec))
			out = ""
			err = fmt.Errorf("ext: %s: command %s failed", inst.manifest.Name, cmd.Name)
		}
	}()
	return cmd.Run(ctx, args)
}

// Observe fans one event out to every running bundled extension's observers,
// in load order. Each observer has a 100 ms budget; a slow one is skipped and
// reported at most once per minute.
func (h *Host) Observe(ev proto.Event) {
	h.mu.Lock()
	insts := append([]*instance(nil), h.insts...)
	h.mu.Unlock()
	for _, inst := range insts {
		inst.mu.Lock()
		fns := append([]eventObserver(nil), inst.events...)
		running := inst.state == stateRunning
		inst.mu.Unlock()
		if !running {
			continue
		}
		for _, fn := range fns {
			h.observeOne(inst, fn, ev)
		}
	}
}

// observeOne runs one observer with a deadline. The observer runs on its own
// goroutine so a misbehaving one cannot stall the event loop. The timer is
// registered before the goroutine starts, so a test can advance a fake clock
// knowing the deadline exists.
func (h *Host) observeOne(inst *instance, fn func(proto.Event), ev proto.Event) {
	timer := h.clock.After(eventCallbackBudget)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if rec := recover(); rec != nil {
				h.reportHookErr(inst, "events", fmt.Errorf("panic: %v", rec))
			}
		}()
		fn(ev)
	}()
	select {
	case <-done:
	case <-timer:
		h.reportHookErr(inst, "events", fmt.Errorf("event observer exceeded %s", eventCallbackBudget))
	}
}
