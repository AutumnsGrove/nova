package testkit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// Call scripts one tool call. When SplitArgs is set, Args are delivered as
// those fragments (the in-process provider still emits one assembled event).
type Call struct {
	ID, Name, Args string
	SplitArgs      []string
}

// args returns the full argument string for the call.
func (c Call) args() string {
	if len(c.SplitArgs) > 0 {
		return strings.Join(c.SplitArgs, "")
	}
	return c.Args
}

// Turn scripts one provider response.
type Turn struct {
	Text       string
	TextChunks []string // when set, used instead of Text
	Reasoning  string
	ToolCalls  []Call
	Usage      *provider.Usage
	Finish     string        // default: "tool_calls" if ToolCalls else "stop"
	Delay      time.Duration // slept via the clock before the first event
	Err        error         // returned after ErrAfter events
	ErrAfter   int
	Hang       bool // block until ctx is cancelled
}

// Provider is a scripted in-process provider. Each Stream call pops the next
// turn and records the request.
type Provider struct {
	clock Clock

	mu    sync.Mutex
	turns []Turn
	next  int
	reqs  []provider.Request
}

// NewProvider returns a provider that plays the given turns in order.
func NewProvider(clock Clock, turns ...Turn) *Provider {
	return &Provider{clock: clock, turns: turns}
}

// Name is the provider id used in tests.
func (p *Provider) Name() string { return "testkit" }

// Remaining reports how many turns are left.
func (p *Provider) Remaining() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.turns) - p.next
}

// Requests returns the recorded requests, deep-copied so callers cannot mutate
// what the provider saw.
func (p *Provider) Requests() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]provider.Request, len(p.reqs))
	for i, r := range p.reqs {
		out[i] = cloneRequest(r)
	}
	return out
}

// Stream plays the next turn: reasoning, text, tool calls, then usage. It
// honours ctx for Delay and Hang, and returns an error once the script is
// exhausted.
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	p.mu.Lock()
	if p.next >= len(p.turns) {
		p.mu.Unlock()
		return nil, errors.New("testkit: script exhausted")
	}
	turn := p.turns[p.next]
	p.next++
	p.reqs = append(p.reqs, cloneRequest(req))
	p.mu.Unlock()

	if turn.Delay > 0 {
		if err := p.clock.Sleep(ctx, turn.Delay); err != nil {
			return nil, err
		}
	}
	if turn.Hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	emit := func(ev provider.StreamEvent) {
		if onEvent != nil {
			onEvent(ev)
		}
	}
	delivered := 0
	checkErr := func() error {
		if turn.Err != nil && delivered >= turn.ErrAfter {
			return turn.Err
		}
		return nil
	}
	if err := checkErr(); err != nil {
		return nil, err
	}

	if turn.Reasoning != "" {
		emit(provider.StreamEvent{Type: provider.StreamReasoningDelta, Text: turn.Reasoning})
		delivered++
		if err := checkErr(); err != nil {
			return nil, err
		}
	}

	chunks := turn.TextChunks
	if len(chunks) == 0 && turn.Text != "" {
		chunks = []string{turn.Text}
	}
	content := strings.Join(chunks, "")
	for _, chunk := range chunks {
		emit(provider.StreamEvent{Type: provider.StreamTextDelta, Text: chunk})
		delivered++
		if err := checkErr(); err != nil {
			return nil, err
		}
	}

	calls := make([]message.ToolCall, 0, len(turn.ToolCalls))
	for _, c := range turn.ToolCalls {
		call := message.ToolCall{
			ID:       c.ID,
			Type:     "function",
			Function: message.FunctionCall{Name: c.Name, Arguments: c.args()},
		}
		emit(provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &call})
		delivered++
		calls = append(calls, call)
		if err := checkErr(); err != nil {
			return nil, err
		}
	}

	if turn.Usage != nil {
		emit(provider.StreamEvent{Type: provider.StreamUsage, Usage: turn.Usage})
		delivered++
		if err := checkErr(); err != nil {
			return nil, err
		}
	}

	finish := turn.Finish
	if finish == "" {
		if len(turn.ToolCalls) > 0 {
			finish = "tool_calls"
		} else {
			finish = "stop"
		}
	}
	var usage provider.Usage
	if turn.Usage != nil {
		usage = *turn.Usage
	}
	return &provider.Response{
		Message: message.Message{
			Role:      message.RoleAssistant,
			Content:   content,
			Reasoning: turn.Reasoning,
			ToolCalls: calls,
		},
		FinishReason: provider.NormalizeFinish(finish),
		Usage:        usage,
		Model:        req.Model,
	}, nil
}

// cloneRequest deep-copies the parts a caller could mutate after a call.
func cloneRequest(r provider.Request) provider.Request {
	out := r
	out.Messages = make([]message.Message, len(r.Messages))
	for i, m := range r.Messages {
		out.Messages[i] = m.Clone()
	}
	out.Tools = append([]provider.ToolDef(nil), r.Tools...)
	out.Stop = append([]string(nil), r.Stop...)
	out.ProviderOptions = cloneAnyMap(r.ProviderOptions)
	if r.ExtraHeaders != nil {
		out.ExtraHeaders = make(map[string]string, len(r.ExtraHeaders))
		for k, v := range r.ExtraHeaders {
			out.ExtraHeaders[k] = v
		}
	}
	return out
}

func cloneAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneAny(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneAnyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneAny(e)
		}
		return out
	default:
		return v
	}
}
