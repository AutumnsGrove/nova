package provider

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/AutumnsGrove/nova/message"
)

// Collector builds a Response from the deltas of one streamed completion and
// hands each delta to the caller as a StreamEvent. All three wire dialects feed
// one, so the rules for text, reasoning, usage, tool-call assembly and finish
// reasons are written once. It is not safe for concurrent use.
type Collector struct {
	resp            Response
	text, reasoning strings.Builder
	calls           map[int]*callAcc
	onEvent         func(StreamEvent)
}

// callAcc accumulates one tool call across its fragments.
type callAcc struct {
	id, typ, name string
	args          strings.Builder
}

// NewCollector starts a response for model. onEvent may be nil.
func NewCollector(model string, onEvent func(StreamEvent)) *Collector {
	c := &Collector{calls: map[int]*callAcc{}, onEvent: onEvent}
	c.resp.Model = model
	c.resp.Message.Role = message.RoleAssistant
	return c
}

func (c *Collector) emit(ev StreamEvent) {
	if c.onEvent != nil {
		c.onEvent(ev)
	}
}

// SetModel records the model name the server reports, when it reports one.
func (c *Collector) SetModel(model string) {
	if model != "" {
		c.resp.Model = model
	}
}

// Text appends assistant text and delivers it.
func (c *Collector) Text(s string) {
	if s == "" {
		return
	}
	c.text.WriteString(s)
	c.emit(StreamEvent{Type: StreamTextDelta, Text: s})
}

// Reasoning appends reasoning text and delivers it.
func (c *Collector) Reasoning(s string) {
	if s == "" {
		return
	}
	c.reasoning.WriteString(s)
	c.emit(StreamEvent{Type: StreamReasoningDelta, Text: s})
}

// Usage records u as the response's usage, replacing any earlier value, and
// delivers it. Providers report running totals, so the latest report wins.
func (c *Collector) Usage(u Usage) {
	c.resp.Usage = u
	delivered := u
	c.emit(StreamEvent{Type: StreamUsage, Usage: &delivered})
}

// ToolCall folds one fragment into the call at index. The first non-empty id,
// type and name win, because later fragments repeat them or omit them and a
// stray different value must not rename a call already keyed by index. args is
// appended. Nothing is delivered until Finish, so a consumer never sees half
// an argument list.
func (c *Collector) ToolCall(index int, id, typ, name, args string) {
	acc := c.calls[index]
	if acc == nil {
		acc = &callAcc{}
		c.calls[index] = acc
	}
	if acc.id == "" {
		acc.id = id
	}
	if acc.typ == "" {
		acc.typ = typ
	}
	if acc.name == "" {
		acc.name = name
	}
	acc.args.WriteString(args)
}

// Partial returns the response as far as it has been delivered: text and
// reasoning, no tool calls. A failed stream returns it with the error so the
// caller keeps what the model already said, and never a half-built call.
func (c *Collector) Partial() *Response {
	c.resp.Message.Content = c.text.String()
	c.resp.Message.Reasoning = c.reasoning.String()
	return &c.resp
}

// Finish completes the response after a stream that ended cleanly. It
// assembles the tool calls in index order and delivers them, then sets the
// finish reason from the provider's own: a stop that comes with tool calls is
// reported as tool_calls, since that is what the agent has to act on.
func (c *Collector) Finish(reason string) *Response {
	r := c.Partial()
	calls, dropped := c.assemble()
	if dropped > 0 {
		c.emit(StreamEvent{Type: StreamError, Err: fmt.Errorf("provider: dropped %d tool call(s) with no name", dropped)})
	}
	r.Message.ToolCalls = calls
	for _, call := range calls {
		c.emit(StreamEvent{Type: StreamToolCall, ToolCall: &call})
	}
	r.FinishReason = NormalizeFinish(reason)
	if len(calls) > 0 && (r.FinishReason == "" || r.FinishReason == "stop") {
		r.FinishReason = "tool_calls"
	}
	return r
}

// assemble finalizes the calls in index order. A call whose name never arrived
// is dropped and counted, because it cannot be executed. A missing id is
// generated, and so is a repeated one: a tool result is matched to its call by
// id, so two calls sharing one would answer each other.
func (c *Collector) assemble() (calls []message.ToolCall, dropped int) {
	taken := map[string]bool{}
	for _, acc := range c.calls {
		taken[acc.id] = true
	}
	seen := map[string]bool{}
	generated := 0
	for _, idx := range slices.Sorted(maps.Keys(c.calls)) {
		acc := c.calls[idx]
		if acc.name == "" {
			dropped++
			continue
		}
		id := acc.id
		for id == "" || seen[id] {
			id = fmt.Sprintf("call_%d_%d", idx, generated)
			generated++
			if taken[id] {
				id = ""
			}
		}
		seen[id] = true
		args := acc.args.String()
		if args == "" {
			args = "{}"
		}
		typ := acc.typ
		if typ == "" {
			typ = "function"
		}
		calls = append(calls, message.ToolCall{
			ID:       id,
			Type:     typ,
			Function: message.FunctionCall{Name: acc.name, Arguments: args},
		})
	}
	return calls, dropped
}
