// Package subagent is the bundled subagent extension: a `subagent` tool that
// runs a fresh agent loop with a restricted tool set and returns its answer.
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
	"github.com/AutumnsGrove/nova/usage"
)

// Defaults and limits.
const (
	maxDepthCap   = 3
	defaultSlots  = 4
	resultCap     = 20000
	defaultSystem = "You are a sub-agent. Finish the task you are given and report the result concisely."
	defaultTools  = "read,list,glob,grep,web_fetch"
	toolName      = "subagent"
)

// Depth is the child depth carried through tool.Context.Data.
type Depth struct{ N int }

// depthOf reads the depth a parent stamped on the call, or 0.
func depthOf(tctx *tool.Context) int {
	if tctx != nil {
		if d, ok := tctx.Data.(Depth); ok {
			return d.N
		}
	}
	return 0
}

// toolRegistry is the piece of the registry the extension validates against.
type toolRegistry interface {
	Get(name string) (tool.Tool, bool)
}

// Extension is the subagent bundled extension.
type Extension struct {
	env      *ext.Env
	registry toolRegistry
	maxDepth int
	// slots bounds concurrent sub-agents, with one semaphore per depth. A
	// sub-agent holds its slot for as long as it runs, so a single shared
	// semaphore would let parents fill it and then deadlock waiting for the
	// slots their own children need.
	slots [maxDepthCap]chan struct{}
}

// New returns a fresh subagent extension with the default concurrency cap.
func New() *Extension {
	e := &Extension{}
	e.setSlots(defaultSlots)
	return e
}

// setSlots gives every depth a fresh semaphore of n slots.
func (e *Extension) setSlots(n int) {
	for i := range e.slots {
		e.slots[i] = make(chan struct{}, n)
	}
}

// Name is the extension name.
func (e *Extension) Name() string { return toolName }

// Init registers the subagent tool and reads the config limits.
func (e *Extension) Init(env *ext.Env) error {
	e.env = env
	e.maxDepth = 1
	if env.Config != nil {
		if d := env.Config.Subagent.MaxDepth; d > 0 {
			e.maxDepth = d
		}
		if n := env.Config.Subagent.MaxConcurrent; n > 0 {
			e.setSlots(n)
		}
	}
	if e.maxDepth > maxDepthCap {
		e.maxDepth = maxDepthCap
	}
	if env.Extra != nil {
		if reg, ok := env.Extra["tools"].(toolRegistry); ok {
			e.registry = reg
		}
	}
	return env.Tool(tool.New(tool.Info{
		Name:        toolName,
		Description: "Run a sub-agent with a restricted tool set and return its concise answer.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":      map[string]any{"type": "string"},
				"tools":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"model":     map[string]any{"type": "string"},
				"max_turns": map[string]any{"type": "integer"},
			},
			"required": []string{"task"},
		},
		ReadOnly: false,
		Parallel: true,
	}, e.run))
}

// run resolves the tool set, runs the child through Spawn, and shapes the
// result.
func (e *Extension) run(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
	var in struct {
		Task     string   `json:"task"`
		Tools    []string `json:"tools"`
		Model    string   `json:"model"`
		MaxTurns int      `json:"max_turns"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Errorf("invalid arguments: %v", err), nil
	}
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return tool.Errorf("task is required"), nil
	}
	depth := depthOf(tctx)
	tools, err := e.resolveTools(in.Tools, depth)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if e.env == nil || e.env.Spawn == nil {
		return tool.Errorf("no sub-agent runner is available"), nil
	}
	// max_turns is optional: no default and no cap. A caller that wants a bound
	// passes one, and 0 means the child runs until it finishes.
	sem := e.slots[min(depth, len(e.slots)-1)]
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return tool.Errorf("cancelled"), nil
	}
	defer func() { <-sem }()

	started := time.Now()
	res, err := e.env.Spawn(ctx, ext.SpawnRequest{
		System:   defaultSystem,
		Task:     task,
		Tools:    tools,
		Model:    in.Model,
		MaxTurns: in.MaxTurns,
		Depth:    depth,
		// Only tool_call names the tool; tool_start carries just the call id.
		OnEvent: func(ev proto.Event) {
			if ev.Type == proto.EventToolCall && ev.Call != nil {
				tctx.Progress("▸ " + ev.Call.Name)
			}
		},
	})
	// A child that failed or was aborted still spent tokens, so the run is
	// charged for them before the failure is reported.
	if res != nil && tctx != nil && tctx.AddUsage != nil {
		tctx.AddUsage(res.Usage)
	}
	if err != nil {
		if ctx.Err() != nil {
			return tool.Errorf("cancelled"), nil
		}
		return tool.Errorf("sub-agent failed: %v", err), nil
	}
	if res == nil {
		return tool.Errorf("sub-agent returned no result"), nil
	}
	tctx.Progress(fmt.Sprintf("done after %d turn(s)", res.Turns))
	content, truncated := cutAnswer(res.Answer)
	if res.Reason == "exhausted" {
		content += "\n(sub-agent exhausted its turn budget)"
	}
	details, _ := json.Marshal(map[string]any{
		"turns":        res.Turns,
		"tools":        res.Tools,
		"duration_ms":  time.Since(started).Milliseconds(),
		"usage":        res.Usage,
		"reason":       res.Reason,
		"nested_usage": res.Usage,
	})
	return tool.Result{Content: content, Truncated: truncated, Details: details}, nil
}

// cutAnswer cuts an answer to resultCap bytes without splitting a character.
func cutAnswer(s string) (string, bool) {
	if len(s) <= resultCap {
		return s, false
	}
	n := resultCap
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// resolveTools validates the requested tools. An empty request uses the
// read-only default, filtered to what the registry actually has.
func (e *Extension) resolveTools(requested []string, depth int) ([]string, error) {
	allowSub := depth < e.maxDepth
	if len(requested) == 0 {
		var out []string
		for _, name := range strings.Split(defaultTools, ",") {
			if e.registry == nil {
				out = append(out, name)
				continue
			}
			if _, ok := e.registry.Get(name); ok {
				out = append(out, name)
			}
		}
		return out, nil
	}
	var out []string
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if name == toolName {
			if !allowSub {
				return nil, errors.New("subagent is not available at this depth")
			}
			out = append(out, name)
			continue
		}
		if e.registry != nil {
			if _, ok := e.registry.Get(name); !ok {
				return nil, fmt.Errorf("unknown tool %q", name)
			}
		}
		out = append(out, name)
	}
	return out, nil
}

// Spawner runs child agent loops. The host builds one and passes its Spawn
// method to the extension's Env.
type Spawner struct {
	Provider   provider.Provider
	Model      string
	Registry   *tool.Registry
	Policy     *permission.Policy
	Ask        func(ctx context.Context, q agent.Question) (bool, error)
	ToolCall   func(ctx context.Context, c agent.ToolCall) *agent.CallDecision
	ToolResult func(ctx context.Context, c agent.ToolCall, r tool.Result) tool.Result
	WorkDir    string
	Roots      []string
	SessionID  string
	Complete   func(ctx context.Context, req provider.Request) (*provider.Response, error)
	Ledger     *usage.Ledger
	MaxDepth   int
	// MaxTokens caps each child request; zero leaves the choice to the provider.
	MaxTokens int
	Clock     agent.Clock
	Emit      func(proto.Event)
}

// Spawn runs one child loop and returns its result.
func (s *Spawner) Spawn(ctx context.Context, req ext.SpawnRequest) (*ext.SpawnResult, error) {
	if s.Provider == nil {
		return nil, errors.New("subagent: no provider")
	}
	maxDepth := min(max(s.MaxDepth, 1), maxDepthCap)
	if req.Depth >= maxDepth {
		return nil, fmt.Errorf("subagent: depth limit of %d reached", maxDepth)
	}
	model := req.Model
	if model == "" {
		model = s.Model
	}
	names := req.Tools
	if len(names) == 0 {
		names = strings.Split(defaultTools, ",")
	}
	child := tool.NewRegistry()
	if s.Registry != nil {
		child = s.Registry.View(names...)
	}
	var mu sync.Mutex
	used := []string{}
	seen := map[string]bool{}
	hooks := agent.Hooks{
		ToolCall: func(ctx context.Context, c agent.ToolCall) *agent.CallDecision {
			mu.Lock()
			if !seen[c.Name] {
				seen[c.Name] = true
				used = append(used, c.Name)
			}
			mu.Unlock()
			if s.ToolCall != nil {
				return s.ToolCall(ctx, c)
			}
			return nil
		},
		ToolResult: s.ToolResult,
	}
	ag := agent.New(agent.Config{
		Provider:  s.Provider,
		Model:     model,
		System:    req.System,
		Tools:     child,
		Policy:    s.Policy,
		Ask:       s.prefixedAsk(),
		WorkDir:   s.WorkDir,
		Roots:     s.Roots,
		SessionID: s.SessionID,
		MaxTurns:  req.MaxTurns,
		MaxTokens: s.MaxTokens,
		Clock:     s.Clock,
		ToolData:  Depth{N: req.Depth + 1},
		Complete:  s.Complete,
		Hooks:     hooks,
		Emit:      s.emit(req.OnEvent),
	})
	res, err := ag.Run(ctx, nil, message.User(req.Task))
	if res == nil {
		return nil, err
	}
	if s.Ledger != nil {
		s.Ledger.Add(model, res.Usage)
	}
	mu.Lock()
	tools := append([]string(nil), used...)
	mu.Unlock()
	// A failed run still returns what it spent, so the caller can account for it.
	return &ext.SpawnResult{Answer: res.Answer, Turns: res.Turns, Usage: res.Usage, Reason: res.Reason, Tools: tools}, err
}

// prefixedAsk marks a child's permission question as coming from a sub-agent.
func (s *Spawner) prefixedAsk() func(context.Context, agent.Question) (bool, error) {
	if s.Ask == nil {
		return nil
	}
	return func(ctx context.Context, q agent.Question) (bool, error) {
		q.Reason = "[subagent] " + q.Reason
		return s.Ask(ctx, q)
	}
}

// hostVisible reports whether a child event may join the host's event stream.
// A child's run, turn, message, and usage events carry run ids that collide with
// the parent's, and a subscriber ends the whole request at the first run_end, so
// only events that stand on their own are passed on.
func hostVisible(typ string) bool {
	return typ == proto.EventPermission || typ == proto.EventExtensionError
}

// emit forwards every child event to the caller's observer, and the events that
// make sense on their own to the host's stream.
func (s *Spawner) emit(onEvent func(proto.Event)) func(proto.Event) {
	if onEvent == nil && s.Emit == nil {
		return nil
	}
	return func(ev proto.Event) {
		if onEvent != nil {
			onEvent(ev)
		}
		if s.Emit != nil && hostVisible(ev.Type) {
			s.Emit(ev)
		}
	}
}
