package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// hookInstances snapshots the running instances that answer hook, in order.
func (h *Host) hookInstances(hook string) []*instance {
	h.mu.Lock()
	insts := append([]*instance(nil), h.insts...)
	h.mu.Unlock()
	var out []*instance
	for _, inst := range insts {
		if inst.declaresHook(hook) {
			out = append(out, inst)
		}
	}
	return out
}

// declaresHook reports whether a running instance answers hook.
func (inst *instance) declaresHook(hook string) bool {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.state != stateRunning {
		return false
	}
	if inst.bundleHooks[hook] {
		return true
	}
	for _, name := range inst.reg.hooks {
		if name == hook {
			return true
		}
	}
	return false
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

// callBundledHook runs a bundled hook with panic recovery. It reports false
// when the hook panicked, so the caller can ignore a zero result.
func (h *Host) callBundledHook(inst *instance, hook string, fn func()) (ok bool) {
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
	reply, err := inst.conn.request(ctx, &outFrame{
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
	for _, inst := range h.hookInstances("tool_call") {
		if fns, bundled := inst.bundleHook(); bundled {
			if fns.ToolCall == nil {
				continue
			}
			var dec *agent.CallDecision
			if !h.callBundledHook(inst, "tool_call", func() {
				dec = fns.ToolCall(ctx, agent.ToolCall{ID: c.ID, Name: c.Name, Arguments: args})
			}) || dec == nil {
				continue
			}
			if dec.Block {
				return &agent.CallDecision{Block: true, Reason: dec.Reason}
			}
			if len(dec.Arguments) > 0 {
				args = dec.Arguments
				changed = true
			}
			continue
		}
		var res toolCallResult
		if !h.callHook(ctx, inst, "tool_call", toolCallEvent{Tool: c.Name, Arguments: args, CallID: c.ID}, &res) {
			continue
		}
		if res.Block {
			return &agent.CallDecision{Block: true, Reason: res.Reason}
		}
		if len(res.Arguments) > 0 {
			args = res.Arguments
			changed = true
		}
	}
	if dec, written := h.runCallCommandHooks(ctx, c.Name, args, c.ID); dec != nil {
		return dec
	} else if written != nil {
		args = written
		changed = true
	}
	if changed {
		return &agent.CallDecision{Arguments: args}
	}
	return nil
}

// hookToolResult composes tool_result hooks as a chain.
func (h *Host) hookToolResult(ctx context.Context, c agent.ToolCall, r tool.Result) tool.Result {
	out := r
	for _, inst := range h.hookInstances("tool_result") {
		if fns, bundled := inst.bundleHook(); bundled {
			if fns.ToolResult == nil {
				continue
			}
			var next tool.Result
			if h.callBundledHook(inst, "tool_result", func() {
				next = fns.ToolResult(ctx, c, out)
			}) {
				out = next
			}
			continue
		}
		event := hookWire{Tool: c.Name, Result: &toolResult{Content: out.Content, IsError: out.IsError, Details: out.Details, Parts: out.Parts}}
		var res struct {
			Result *toolResult `json:"result"`
		}
		if !h.callHook(ctx, inst, "tool_result", event, &res) || res.Result == nil {
			continue
		}
		out = tool.Result{Content: res.Result.Content, IsError: res.Result.IsError, Details: res.Result.Details, Parts: res.Result.Parts}
	}
	return out
}

// hookTransformContext composes transform_context hooks as a chain.
func (h *Host) hookTransformContext(ctx context.Context, msgs []message.Message) []message.Message {
	out := msgs
	for _, inst := range h.hookInstances("transform_context") {
		if fns, bundled := inst.bundleHook(); bundled {
			if fns.TransformContext == nil {
				continue
			}
			var next []message.Message
			if h.callBundledHook(inst, "transform_context", func() {
				next = fns.TransformContext(ctx, out)
			}) && next != nil {
				out = next
			}
			continue
		}
		event := hookWire{Messages: out}
		var res hookWire
		if !h.callHook(ctx, inst, "transform_context", event, &res) || res.Messages == nil {
			continue
		}
		out = res.Messages
	}
	return out
}

// hookBeforeProviderRequest runs every before_provider_request hook in order.
func (h *Host) hookBeforeProviderRequest(ctx context.Context, req *provider.Request) {
	current := *req
	for _, inst := range h.hookInstances("before_provider_request") {
		if fns, bundled := inst.bundleHook(); bundled {
			if fns.BeforeProviderRequest == nil {
				continue
			}
			local := current
			if h.callBundledHook(inst, "before_provider_request", func() {
				fns.BeforeProviderRequest(ctx, &local)
			}) {
				current = local
			}
			continue
		}
		event := hookWire{Request: &current}
		var res hookWire
		if !h.callHook(ctx, inst, "before_provider_request", event, &res) || res.Request == nil {
			continue
		}
		current = *res.Request
	}
	*req = current
}

// hookMessageEnd composes message_end hooks as a chain.
func (h *Host) hookMessageEnd(ctx context.Context, m message.Message) message.Message {
	out := m
	for _, inst := range h.hookInstances("message_end") {
		if fns, bundled := inst.bundleHook(); bundled {
			if fns.MessageEnd == nil {
				continue
			}
			var next message.Message
			if h.callBundledHook(inst, "message_end", func() {
				next = fns.MessageEnd(ctx, out)
			}) {
				out = next
			}
			continue
		}
		event := hookWire{Message: &out}
		var res hookWire
		if !h.callHook(ctx, inst, "message_end", event, &res) || res.Message == nil {
			continue
		}
		out = *res.Message
	}
	return out
}

// hookTurnEnd merges End, Continue, and Inject across every extension.
func (h *Host) hookTurnEnd(ctx context.Context, t agent.Turn) agent.TurnDecision {
	var decision agent.TurnDecision
	results := make([]toolResult, 0, len(t.Results))
	for _, r := range t.Results {
		results = append(results, toolResult{Content: r.Content, IsError: r.IsError, Details: r.Details})
	}
	for _, inst := range h.hookInstances("turn_end") {
		if fns, bundled := inst.bundleHook(); bundled {
			if fns.TurnEnd == nil {
				continue
			}
			var res agent.TurnDecision
			if !h.callBundledHook(inst, "turn_end", func() { res = fns.TurnEnd(ctx, t) }) {
				continue
			}
			if res.End {
				decision.End = true
			}
			if res.Continue {
				decision.Continue = true
			}
			decision.Inject = append(decision.Inject, res.Inject...)
			continue
		}
		event := hookWire{Turn: t.Index, Message: &t.Message, Results: results}
		var res hookWire
		if !h.callHook(ctx, inst, "turn_end", event, &res) {
			continue
		}
		if res.End {
			decision.End = true
		}
		if res.Continue {
			decision.Continue = true
		}
		decision.Inject = append(decision.Inject, res.Inject...)
	}
	return decision
}

// hookAgentEnd runs every agent_end observer. Observers cannot change a run.
func (h *Host) hookAgentEnd(ctx context.Context, r *agent.Result) {
	for _, inst := range h.hookInstances("agent_end") {
		if fns, bundled := inst.bundleHook(); bundled {
			if fns.AgentEnd == nil {
				continue
			}
			h.callBundledHook(inst, "agent_end", func() { fns.AgentEnd(ctx, r) })
			continue
		}
		event := map[string]any{"reason": r.Reason, "turns": r.Turns, "answer": r.Answer}
		_ = h.callHook(ctx, inst, "agent_end", event, &map[string]any{})
	}
}

// runCallCommandHooks runs every matching tool_call command hook. It returns a
// block decision or rewritten arguments, whichever a script produced.
func (h *Host) runCallCommandHooks(ctx context.Context, toolName string, args json.RawMessage, callID string) (*agent.CallDecision, json.RawMessage) {
	h.mu.Lock()
	insts := append([]*instance(nil), h.insts...)
	h.mu.Unlock()
	event := toolCallEvent{Tool: toolName, Arguments: args, CallID: callID}
	var written json.RawMessage
	for _, inst := range insts {
		inst.mu.Lock()
		running := inst.state == stateRunning
		specs := append([]HookSpec(nil), inst.manifest.Hooks...)
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

// matchHook applies a command hook's match map to a tool call.
func matchHook(spec HookSpec, ev toolCallEvent) bool {
	for key, pattern := range spec.Match {
		value := ""
		if key == "tool" {
			value = ev.Tool
		} else {
			var m map[string]any
			_ = json.Unmarshal(ev.Arguments, &m)
			value, _ = m[key].(string)
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
	stdout, code, err := runHookScript(ctx, inst.manifest.Dir, spec.Run, payload, commandHookTimeout)
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
func runHookScript(ctx context.Context, dir string, argv []string, stdin []byte, timeout time.Duration) (string, int, error) {
	if len(argv) == 0 {
		return "", 0, errors.New("empty command")
	}
	call, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := groupCommand(call, resolveExec(dir, argv[0]), argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	out := newCapBuffer(maxHookOutputBytes)
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		if call.Err() != nil {
			return string(out.bytes()), -1, call.Err()
		}
		return string(out.bytes()), exitCode(err), nil
	}
	return string(out.bytes()), 0, nil
}
