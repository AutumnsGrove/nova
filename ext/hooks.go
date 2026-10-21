package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// Hook deadlines and the rate limit for hook error reports.
const (
	hookTimeout        = 2 * time.Second
	hookErrorInterval  = time.Minute
	commandHookTimeout = 10 * time.Second
	// maxHookOutputBytes bounds what a command hook may print, so a runaway
	// script cannot exhaust memory. A decision is a few hundred bytes.
	maxHookOutputBytes = 1 << 20
)

// toolCallEvent and toolCallResult are the tool_call hook's wire shapes.
type toolCallEvent struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
}

type toolCallResult struct {
	Block     bool            `json:"block,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// toolResult is the result payload shared by the tool_result hook.
type toolResult struct {
	Content string                `json:"content"`
	IsError bool                  `json:"is_error,omitempty"`
	Details json.RawMessage       `json:"details,omitempty"`
	Parts   []message.ContentPart `json:"parts,omitempty"`
}

// hookWire is the generic event/result envelope for the chain hooks.
type hookWire struct {
	Tool      string            `json:"tool,omitempty"`
	Arguments json.RawMessage   `json:"arguments,omitempty"`
	Result    *toolResult       `json:"result,omitempty"`
	Messages  []message.Message `json:"messages,omitempty"`
	Message   *message.Message  `json:"message,omitempty"`
	Request   *provider.Request `json:"request,omitempty"`
	Results   []toolResult      `json:"results,omitempty"`
	Turn      int               `json:"turn,omitempty"`
	End       bool              `json:"end,omitempty"`
	Continue  bool              `json:"continue,omitempty"`
	Inject    []message.Message `json:"inject,omitempty"`
}

// Hooks returns the composed hooks for every running extension.
func (h *Host) Hooks() agent.Hooks {
	return agent.Hooks{
		ToolCall:              h.hookToolCall,
		ToolResult:            h.hookToolResult,
		TransformContext:      h.hookTransformContext,
		BeforeProviderRequest: h.hookBeforeProviderRequest,
		MessageEnd:            h.hookMessageEnd,
		TurnEnd:               h.hookTurnEnd,
		AgentEnd:              h.hookAgentEnd,
	}
}

// hookSource is one extension that answers a hook, with the functions that
// answer it.
type hookSource struct {
	inst  *instance
	hooks agent.Hooks
}

// hookSources lists, in load order, the running extensions that answer hook. A
// bundled extension answers with its own Go hooks and a subprocess one with
// functions that send the hook over its connection, so the composition below
// treats both alike.
func (h *Host) hookSources(hook string) []hookSource {
	var out []hookSource
	for _, inst := range h.snapshot() {
		if !inst.declaresHook(hook) {
			continue
		}
		if fns, bundled := inst.bundleHook(); bundled {
			out = append(out, hookSource{inst, fns})
		} else {
			out = append(out, hookSource{inst, h.processHooks(inst)})
		}
	}
	return out
}

// declaresHook reports whether a running instance answers hook.
func (inst *instance) declaresHook(hook string) bool {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.state == stateRunning && (inst.bundleHooks[hook] || slices.Contains(inst.reg.hooks, hook))
}

// bundleHook snapshots a bundled instance's hooks. ok is false for a subprocess
// extension.
func (inst *instance) bundleHook() (agent.Hooks, bool) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.bundled == nil {
		return agent.Hooks{}, false
	}
	return inst.envHooks, true
}

// processHooks adapts a subprocess extension to the loop's hook functions. A
// timeout, a down extension, or an invalid answer leaves the value unchanged.
func (h *Host) processHooks(inst *instance) agent.Hooks {
	call := func(ctx context.Context, hook string, event, out any) bool {
		return h.callHook(ctx, inst, hook, event, out)
	}
	return agent.Hooks{
		ToolCall: func(ctx context.Context, c agent.ToolCall) *agent.CallDecision {
			var res toolCallResult
			if !call(ctx, "tool_call", toolCallEvent{Tool: c.Name, Arguments: c.Arguments, CallID: c.ID}, &res) {
				return nil
			}
			return &agent.CallDecision{Block: res.Block, Reason: res.Reason, Arguments: res.Arguments}
		},
		ToolResult: func(ctx context.Context, c agent.ToolCall, r tool.Result) tool.Result {
			var res struct {
				Result *toolResult `json:"result"`
			}
			event := hookWire{Tool: c.Name, Result: &toolResult{Content: r.Content, IsError: r.IsError, Details: r.Details, Parts: r.Parts}}
			if !call(ctx, "tool_result", event, &res) || res.Result == nil {
				return r
			}
			return tool.Result{Content: res.Result.Content, IsError: res.Result.IsError, Details: res.Result.Details, Parts: res.Result.Parts, Truncated: r.Truncated}
		},
		TransformContext: func(ctx context.Context, msgs []message.Message) []message.Message {
			var res hookWire
			if !call(ctx, "transform_context", hookWire{Messages: msgs}, &res) || res.Messages == nil {
				return msgs
			}
			return res.Messages
		},
		BeforeProviderRequest: func(ctx context.Context, req *provider.Request) {
			var res hookWire
			if call(ctx, "before_provider_request", hookWire{Request: req}, &res) && res.Request != nil {
				*req = *res.Request
			}
		},
		MessageEnd: func(ctx context.Context, m message.Message) message.Message {
			var res hookWire
			if !call(ctx, "message_end", hookWire{Message: &m}, &res) || res.Message == nil {
				return m
			}
			return *res.Message
		},
		TurnEnd: func(ctx context.Context, t agent.Turn) agent.TurnDecision {
			results := make([]toolResult, 0, len(t.Results))
			for _, r := range t.Results {
				results = append(results, toolResult{Content: r.Content, IsError: r.IsError, Details: r.Details})
			}
			var res hookWire
			if !call(ctx, "turn_end", hookWire{Turn: t.Index, Message: &t.Message, Results: results}, &res) {
				return agent.TurnDecision{}
			}
			return agent.TurnDecision{End: res.End, Continue: res.Continue, Inject: res.Inject}
		},
		AgentEnd: func(ctx context.Context, r *agent.Result) {
			event := map[string]any{"reason": r.Reason, "turns": r.Turns, "answer": r.Answer}
			call(ctx, "agent_end", event, &map[string]any{})
		},
	}
}

// guard runs fn, which calls an extension's hook, and recovers a panic in a
// bundled extension's Go code. It reports false when fn panicked, so the caller
// ignores whatever fn left behind.
func (h *Host) guard(inst *instance, hook string, fn func()) (ok bool) {
	ok = true
	defer func() {
		if rec := recover(); rec != nil {
			h.reportHookErr(inst, hook, fmt.Errorf("panic: %v", rec))
			ok = false
		}
	}()
	fn()
	return ok
}

// callHook sends one protocol hook and decodes its result. It reports false on
// a timeout, a down extension, or an invalid result.
func (h *Host) callHook(ctx context.Context, inst *instance, hook string, event any, out any) bool {
	raw, err := json.Marshal(event)
	if err != nil {
		return false
	}
	reply, err := inst.request(ctx, &outFrame{
		Type:       "hook",
		Hook:       hook,
		Event:      raw,
		DeadlineMS: int(hookTimeout / time.Millisecond),
	}, hookTimeout)
	if err != nil {
		if !errors.Is(err, errCancelled) {
			h.reportHookErr(inst, hook, err)
		}
		return false
	}
	if reply.Error != nil {
		h.reportHookErr(inst, hook, errors.New(reply.Error.Message))
		return false
	}
	if len(reply.Result) == 0 || string(reply.Result) == "null" {
		return false
	}
	if err := json.Unmarshal(reply.Result, out); err != nil {
		h.reportHookErr(inst, hook, err)
		return false
	}
	return true
}

// reportHookErr emits an extension_error, at most once per hook per minute.
func (h *Host) reportHookErr(inst *instance, hook string, err error) {
	key := inst.manifest.Name + "\x00" + hook
	h.mu.Lock()
	last := h.hookErrAt[key]
	now := h.clock.Now()
	due := now.Sub(last) >= hookErrorInterval
	if due {
		h.hookErrAt[key] = now
	}
	h.mu.Unlock()
	if due {
		h.emitError(inst, hook, err.Error())
	}
}

// hookToolCall composes tool_call hooks: first block wins, rewrites chain.
func (h *Host) hookToolCall(ctx context.Context, c agent.ToolCall) *agent.CallDecision {
	args := c.Arguments
	changed := false
	for _, src := range h.hookSources("tool_call") {
		var dec *agent.CallDecision
		if !h.guard(src.inst, "tool_call", func() {
			dec = src.hooks.ToolCall(ctx, agent.ToolCall{ID: c.ID, Name: c.Name, Arguments: args})
		}) || dec == nil {
			continue
		}
		if dec.Block {
			return &agent.CallDecision{Block: true, Reason: dec.Reason}
		}
		if len(dec.Arguments) > 0 {
			args, changed = dec.Arguments, true
		}
	}
	if dec, written := h.runCallCommandHooks(ctx, c.Name, args, c.ID); dec != nil {
		return dec
	} else if written != nil {
		args, changed = written, true
	}
	if changed {
		return &agent.CallDecision{Arguments: args}
	}
	return nil
}

// hookToolResult composes tool_result hooks as a chain.
func (h *Host) hookToolResult(ctx context.Context, c agent.ToolCall, r tool.Result) tool.Result {
	out := r
	for _, src := range h.hookSources("tool_result") {
		var next tool.Result
		if h.guard(src.inst, "tool_result", func() { next = src.hooks.ToolResult(ctx, c, out) }) {
			out = next
		}
	}
	return out
}

// hookTransformContext composes transform_context hooks as a chain.
func (h *Host) hookTransformContext(ctx context.Context, msgs []message.Message) []message.Message {
	out := msgs
	for _, src := range h.hookSources("transform_context") {
		var next []message.Message
		if h.guard(src.inst, "transform_context", func() { next = src.hooks.TransformContext(ctx, out) }) && next != nil {
			out = next
		}
	}
	return out
}

// hookBeforeProviderRequest runs every before_provider_request hook in order.
func (h *Host) hookBeforeProviderRequest(ctx context.Context, req *provider.Request) {
	current := *req
	for _, src := range h.hookSources("before_provider_request") {
		local := current
		if h.guard(src.inst, "before_provider_request", func() { src.hooks.BeforeProviderRequest(ctx, &local) }) {
			current = local
		}
	}
	*req = current
}

// hookMessageEnd composes message_end hooks as a chain.
func (h *Host) hookMessageEnd(ctx context.Context, m message.Message) message.Message {
	out := m
	for _, src := range h.hookSources("message_end") {
		var next message.Message
		if h.guard(src.inst, "message_end", func() { next = src.hooks.MessageEnd(ctx, out) }) {
			out = next
		}
	}
	return out
}

// hookTurnEnd merges End, Continue, and Inject across every extension.
func (h *Host) hookTurnEnd(ctx context.Context, t agent.Turn) agent.TurnDecision {
	var decision agent.TurnDecision
	for _, src := range h.hookSources("turn_end") {
		var res agent.TurnDecision
		if !h.guard(src.inst, "turn_end", func() { res = src.hooks.TurnEnd(ctx, t) }) {
			continue
		}
		decision.End = decision.End || res.End
		decision.Continue = decision.Continue || res.Continue
		decision.Inject = append(decision.Inject, res.Inject...)
	}
	return decision
}

// hookAgentEnd runs every agent_end observer. Observers cannot change a run.
func (h *Host) hookAgentEnd(ctx context.Context, r *agent.Result) {
	for _, src := range h.hookSources("agent_end") {
		h.guard(src.inst, "agent_end", func() { src.hooks.AgentEnd(ctx, r) })
	}
}

// runCallCommandHooks runs every matching tool_call command hook. It returns a
// block decision or rewritten arguments, whichever a script produced.
func (h *Host) runCallCommandHooks(ctx context.Context, toolName string, args json.RawMessage, callID string) (*agent.CallDecision, json.RawMessage) {
	event := toolCallEvent{Tool: toolName, Arguments: args, CallID: callID}
	var written json.RawMessage
	for _, inst := range h.snapshot() {
		inst.mu.Lock()
		running := inst.state == stateRunning
		specs := slices.Clone(inst.manifest.Hooks)
		inst.mu.Unlock()
		if !running {
			continue
		}
		for _, spec := range specs {
			if spec.On != "tool_call" || !matchHook(spec, event) {
				continue
			}
			res, ok := h.runCommandHook(ctx, inst, spec, event)
			if !ok {
				continue
			}
			if res.Block {
				return &agent.CallDecision{Block: true, Reason: res.Reason}, nil
			}
			if len(res.Arguments) > 0 {
				written = res.Arguments
				event.Arguments = res.Arguments
			}
		}
	}
	return nil, written
}

// matchHook applies a command hook's match map to a tool call: "tool" matches
// the tool name, any other key matches the string argument of that name.
func matchHook(spec HookSpec, ev toolCallEvent) bool {
	var args map[string]any
	for key, pattern := range spec.Match {
		value := ev.Tool
		if key != "tool" {
			if args == nil {
				_ = json.Unmarshal(ev.Arguments, &args)
			}
			value, _ = args[key].(string)
		}
		if !permission.MatchString(pattern, value) {
			return false
		}
	}
	return true
}

// runCommandHook runs one command hook and parses its JSON decision.
func (h *Host) runCommandHook(ctx context.Context, inst *instance, spec HookSpec, event any) (toolCallResult, bool) {
	payload, err := json.Marshal(event)
	if err != nil {
		return toolCallResult{}, false
	}
	stdout, code, err := runHookScript(ctx, inst.manifest.Dir, spec.Run, payload, commandHookTimeout, h.commandEnv())
	if err != nil {
		h.logf("warn", inst.manifest.Name+": command hook failed: "+err.Error())
		return toolCallResult{}, false
	}
	if code != 0 {
		h.logf("warn", inst.manifest.Name+": command hook exited with code "+strconv.Itoa(code))
		return toolCallResult{}, false
	}
	if len(strings.TrimSpace(stdout)) == 0 {
		return toolCallResult{}, false
	}
	var res toolCallResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		h.logf("warn", inst.manifest.Name+": command hook produced malformed JSON")
		return toolCallResult{}, false
	}
	return res, true
}

// runHookScript runs a command hook with the event on stdin and returns its
// stdout and exit code. It runs directly, never through a shell.
func runHookScript(ctx context.Context, dir string, argv []string, stdin []byte, timeout time.Duration, env []string) (string, int, error) {
	if len(argv) == 0 {
		return "", 0, errors.New("empty command")
	}
	call, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := groupCommand(call, resolveExec(dir, argv[0]), argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(stdin)
	out := tool.NewTailBuffer(maxHookOutputBytes)
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		if call.Err() != nil {
			return string(out.Bytes()), -1, call.Err()
		}
		return string(out.Bytes()), exitCode(err), nil
	}
	return string(out.Bytes()), 0, nil
}
