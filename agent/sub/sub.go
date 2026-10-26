// Package sub runs child agent loops from a parent's configuration, so a program
// that embeds Nova can fan out sub-agents without writing the hard parts itself:
// a restricted tool view, isolated per-child state, event tagging, usage that
// folds back into the parent exactly once, depth and concurrency limits, and
// cancellation that follows the parent's context.
//
// The primitive is deliberately below the extension host. `bundled/subagent` is
// a thin wrapper over it, so the in-process and extension paths share one
// implementation instead of two.
package sub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// Limits are the defaults a caller gets before setting its own.
const (
	// DefaultMaxDepth is how many levels of nesting are allowed before a child
	// is refused. Three means a child may spawn a grandchild and a
	// great-grandchild.
	DefaultMaxDepth = 3
	// DefaultMaxConcurrent bounds children in flight at the same depth. Each
	// depth gets its own allowance so a parent cannot fill the pool its own
	// children need.
	DefaultMaxConcurrent = 4
)

// Depth records a child's depth in tool.Context.Data, so a tool that spawns
// knows how deep it already is. The parent stamps Depth{N+1} on a child unless
// Task.Data overrides it.
type Depth struct{ N int }

// DepthOf returns the depth recorded on data, or 0 when it carries none.
func DepthOf(data any) int {
	if d, ok := data.(Depth); ok {
		return d.N
	}
	return 0
}

// Parent is what a child inherits from the run that spawned it. Only Provider,
// Model, and the tool registry are required; everything else is a seam the child
// shares with its parent.
type Parent struct {
	Provider provider.Provider
	Model    string
	// ModelID is the parent's stable catalog id, so a child's requests route the
	// same way. Empty means Model is the catalog id.
	ModelID string
	Tools   *tool.Registry

	Policy *permission.Policy
	Ask    func(ctx context.Context, q agent.Question) (bool, error)
	// ToolCall and ToolResult are the parent's hooks, applied unchanged to the
	// child. The loop's other hooks are not inherited: a TurnEnd hook that ends a
	// run or injects a message belongs to the parent's own conversation.
	ToolCall   func(ctx context.Context, c agent.ToolCall) *agent.CallDecision
	ToolResult func(ctx context.Context, c agent.ToolCall, r tool.Result) tool.Result

	WorkDir   string
	Roots     []string
	SessionID string

	Complete func(ctx context.Context, req provider.Request) (*provider.Response, error)
	Decide   func(ctx context.Context, req decide.Request) (*decide.Response, error)
	// MaxTokens caps each child request.
	MaxTokens int
	Clock     agent.Clock

	// Emit is the parent's event stream. Child events reach it tagged with the
	// child's run id; run-level events never do, because a subscriber treats
	// run_end as the end of the whole request and a child's usage as the
	// parent's own.
	Emit func(proto.Event)
	// AddUsage folds a child's usage into the parent's run totals. It is called
	// once per child that ran, including one that failed after spending tokens.
	AddUsage func(provider.Usage)
}

// Task is one child run.
type Task struct {
	// System and Prompt are the child's system prompt and user task.
	System string
	Prompt string
	// Tools restricts the child's tool view to these names. Empty offers every
	// tool the parent has. A name the parent lacks is simply absent, so a call to
	// it is the ordinary unknown-tool error result.
	Tools []string
	// Model overrides the parent's model. A catalog id re-routes the child; an
	// unknown id is ignored and the parent's routing is kept.
	Model    string
	MaxTurns int

	// Depth is the parent's own depth, so the child runs at Depth+1. A tool reads
	// it from its context with DepthOf(tctx.Data).
	Depth int
	// Data replaces the child's tool.Context.Data. Nil stamps Depth{N: Depth+1}.
	Data any

	// OnEvent receives every child event untagged, including the run lifecycle
	// ones the parent's stream never sees.
	OnEvent func(proto.Event)
}

// Options tune one Spawner. Zero values take the Default* constants.
type Options struct {
	MaxDepth      int
	MaxConcurrent int
	// HostEvents filters which non-run-level events reach Parent.Emit. Nil
	// forwards all of them. An extension that must not interleave its child's
	// text with the parent's conversation narrows it here rather than pushing
	// events back through a second wrapper.
	HostEvents func(typ string) bool
}

// Result is the outcome of one child run.
type Result struct {
	Answer string
	Turns  int
	Usage  provider.Usage
	Reason string
	// Model is the slug the child ran with, after any catalog re-routing, so a
	// caller can price its usage.
	Model string
	// Tools lists the tools the child actually called, in first-use order.
	Tools []string
}

// Spawner runs child loops for one parent. It is safe for concurrent use: the
// concurrency slots and the child-id counter are shared state, and nothing else
// is mutated.
type Spawner struct {
	parent        Parent
	maxDepth      int
	maxConcurrent int
	hostEvents    func(string) bool

	mu    sync.Mutex
	slots map[int]chan struct{}
	next  atomic.Int64
}

// New builds a Spawner from the parent it inherits and the limits it runs under.
func New(p Parent, opt Options) *Spawner {
	if opt.MaxDepth <= 0 {
		opt.MaxDepth = DefaultMaxDepth
	}
	if opt.MaxConcurrent <= 0 {
		opt.MaxConcurrent = DefaultMaxConcurrent
	}
	return &Spawner{
		parent:        p,
		maxDepth:      opt.MaxDepth,
		maxConcurrent: opt.MaxConcurrent,
		hostEvents:    opt.HostEvents,
		slots:         make(map[int]chan struct{}),
	}
}

// ErrDepth reports that a run is already as deep as the limit allows.
var ErrDepth = errors.New("sub: depth limit reached")

// Spawn runs one child loop and waits for it. A child inherits the parent's
// context, so cancelling the parent aborts every child and Spawn returns once
// they have stopped; Spawn starts no goroutine of its own.
//
// A failed or aborted child still reports what it spent: Result is non-nil
// whenever the loop ran, so its usage is folded into the parent either way.
func (s *Spawner) Spawn(ctx context.Context, t Task) (*Result, error) {
	p := s.parent
	if p.Provider == nil {
		return nil, errors.New("sub: the parent has no provider")
	}
	if t.Depth >= s.maxDepth {
		return nil, fmt.Errorf("%w: limit is %d, child would run at %d", ErrDepth, s.maxDepth, t.Depth+1)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Hold a slot at the parent's depth for as long as the child runs. A nested
	// child asks for a different depth's slot, so it never waits on the slot its
	// own parent is holding.
	sem := s.slotFor(t.Depth)
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-sem }()

	cfg, used := s.childConfig(t, s.childID(), t.OnEvent)
	res, err := agent.New(cfg).Run(ctx, nil, message.User(t.Prompt))

	out := &Result{Reason: "error", Model: cfg.Model}
	if res != nil {
		out.Answer, out.Turns, out.Usage, out.Reason = res.Answer, res.Turns, res.Usage, res.Reason
	}
	out.Tools = used.names()
	if p.AddUsage != nil && res != nil {
		p.AddUsage(out.Usage)
	}
	return out, err
}

// childID returns a unique run id for one child. It is flat rather than nested
// so a grandchild's events cannot collide with its parent child's.
func (s *Spawner) childID() string {
	return fmt.Sprintf("c%d", s.next.Add(1))
}

// slotFor returns the semaphore for one depth, creating it on first use.
func (s *Spawner) slotFor(depth int) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.slots[depth]
	if !ok {
		ch = make(chan struct{}, s.maxConcurrent)
		s.slots[depth] = ch
	}
	return ch
}

// childConfig builds the child's agent configuration: the parent's seams, a
// scoped tool view, fresh tool data, and an emit wrapper that tags its events.
// The returned toolUse records what the child called.
func (s *Spawner) childConfig(t Task, id string, onEvent func(proto.Event)) (agent.Config, *toolUse) {
	p := s.parent
	model, modelID := p.Model, p.ModelID
	if t.Model != "" {
		// A catalog id re-routes the child; an id the catalog does not know keeps
		// the parent's routing rather than guessing a provider.
		if m, ok := provider.GetModel(t.Model); ok {
			model, modelID = m.Slug, m.ID
		}
	}
	reg := p.Tools
	if len(t.Tools) > 0 && p.Tools != nil {
		reg = p.Tools.View(t.Tools...)
	}
	data := t.Data
	if data == nil {
		data = Depth{N: t.Depth + 1}
	}
	used := &toolUse{parent: p.ToolCall}
	return agent.Config{
		Provider:  p.Provider,
		Model:     model,
		ModelID:   modelID,
		System:    t.System,
		Tools:     reg,
		Policy:    p.Policy,
		Ask:       p.Ask,
		WorkDir:   p.WorkDir,
		Roots:     p.Roots,
		SessionID: p.SessionID,
		MaxTurns:  t.MaxTurns,
		MaxTokens: p.MaxTokens,
		Clock:     p.Clock,
		ToolData:  data,
		Complete:  p.Complete,
		Decide:    p.Decide,
		Hooks: agent.Hooks{
			ToolCall:   used.call,
			ToolResult: p.ToolResult,
		},
		Emit: s.emitFor(onEvent, id),
	}, used
}

// emitFor returns the child's event sink: the caller's observer sees everything,
// the parent's stream sees only what describes work by an agent rather than a
// run, tagged with the child id.
func (s *Spawner) emitFor(onEvent func(proto.Event), id string) func(proto.Event) {
	if onEvent == nil && s.parent.Emit == nil {
		return nil
	}
	return func(ev proto.Event) {
		if onEvent != nil {
			onEvent(ev)
		}
		if s.parent.Emit == nil || runLevel(ev.Type) {
			return
		}
		if s.hostEvents != nil && !s.hostEvents(ev.Type) {
			return
		}
		ev.Run = id
		s.parent.Emit(ev)
	}
}

// runLevel reports the event types that address the run rather than the agent's
// work. A child's run_end would end the parent's request, its usage would
// overwrite the parent's total, and its message_end would replace the parent's
// answer, so none of them cross.
func runLevel(typ string) bool {
	switch typ {
	case proto.EventRunStart, proto.EventRunEnd,
		proto.EventTurnStart, proto.EventTurnEnd,
		proto.EventMessageStart, proto.EventMessageEnd,
		proto.EventUsage, proto.EventError,
		proto.EventQueueUpdate, proto.EventSessionChanged, proto.EventStateChanged:
		return true
	}
	return false
}

// toolUse records the tools a child called, in first-use order, and forwards to
// the parent's ToolCall hook so the parent's policy still guards the child.
type toolUse struct {
	parent func(ctx context.Context, c agent.ToolCall) *agent.CallDecision

	mu   sync.Mutex
	seen map[string]bool
	used []string
}

func (u *toolUse) call(ctx context.Context, c agent.ToolCall) *agent.CallDecision {
	u.mu.Lock()
	if u.seen == nil {
		u.seen = make(map[string]bool)
	}
	if !u.seen[c.Name] {
		u.seen[c.Name] = true
		u.used = append(u.used, c.Name)
	}
	u.mu.Unlock()
	if u.parent != nil {
		return u.parent(ctx, c)
	}
	return nil
}

// names returns the tools recorded so far.
func (u *toolUse) names() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.used...)
}
