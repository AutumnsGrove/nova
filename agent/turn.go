package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
)

// turnRequest builds the request for one turn, runs the hooks around it, and
// retries a retryable provider failure. It returns the assistant message and
// that response's usage.
func (r *run) turnRequest() (message.Message, provider.Usage, error) {
	if h := r.cfg.Hooks.BeforeTurn; h != nil {
		type outcome struct {
			msgs []message.Message
			err  error
		}
		// Snapshot before spawning: an abandoned hook goroutine must not read
		// r.messages while the loop appends to it.
		snapshot := cloneMessages(r.messages)
		out, ok := runHook(r, "before_turn", func(ctx context.Context) outcome {
			msgs, err := h(ctx, snapshot)
			return outcome{msgs, err}
		})
		if ok {
			if out.err != nil {
				return message.Message{}, provider.Usage{}, out.err
			}
			// A nil replacement means "keep the transcript"; only a real
			// slice replaces it, so a zero-value return cannot wipe state.
			if out.msgs != nil {
				r.messages = out.msgs
			}
		}
	}

	req := provider.Request{
		Model:           r.cfg.Model,
		Messages:        r.messages,
		Temperature:     r.cfg.Temperature,
		MaxTokens:       r.cfg.MaxTokens,
		ReasoningEffort: r.cfg.ReasoningEffort,
		ProviderOptions: r.cfg.ProviderOptions,
		PromptCache:     r.cfg.PromptCache,
	}
	if r.cfg.Tools != nil {
		req.Tools = r.cfg.Tools.Defs()
	}
	if h := r.cfg.Hooks.TransformContext; h != nil {
		snapshot := cloneMessages(r.messages)
		msgs, ok := runHook(r, "transform_context", func(ctx context.Context) []message.Message {
			return h(ctx, snapshot)
		})
		if ok && msgs != nil {
			req.Messages = msgs
		}
	}
	if h := r.cfg.Hooks.BeforeProviderRequest; h != nil {
		next := req
		next.Messages = cloneMessages(req.Messages)
		if _, ok := runHook(r, "before_provider_request", func(ctx context.Context) bool {
			h(ctx, &next)
			return true
		}); ok {
			req = next
		}
	}
	return r.callProvider(r.gateImages(req))
}

// callProvider performs the request and retries a retryable provider error,
// emitting a retry event before each wait. Cancellation is never retried.
func (r *run) callProvider(req provider.Request) (message.Message, provider.Usage, error) {
	max := r.cfg.Retry.Max
	switch {
	case max < 0:
		max = 0
	case max == 0:
		max = defaultRetryMax
	}
	for attempt := 0; ; attempt++ {
		msg, usage, err := r.streamOnce(req)
		if err == nil {
			return msg, usage, nil
		}
		if r.ctx.Err() != nil {
			return message.Message{}, provider.Usage{}, r.ctx.Err()
		}
		var pe *provider.Error
		if attempt >= max || !errors.As(err, &pe) || !pe.Retryable {
			return message.Message{}, provider.Usage{}, err
		}
		delay := retryDelay(r.cfg.Retry, pe, attempt)
		r.emit(proto.Event{Type: proto.EventRetry, Attempt: attempt + 1, Max: max, DelayMS: int(delay.Milliseconds()), Error: &proto.Error{Code: proto.CodeProviderError, Message: err.Error()}})
		if serr := r.a.clock.Sleep(r.ctx, delay); serr != nil {
			return message.Message{}, provider.Usage{}, serr
		}
	}
}

// streamOnce performs one provider request and emits every per-attempt event:
// message_start, thinking/text deltas, tool_call, message_end, and usage.
func (r *run) streamOnce(req provider.Request) (message.Message, provider.Usage, error) {
	var (
		text      strings.Builder
		reasoning strings.Builder
		calls     []message.ToolCall
		seen      = map[string]bool{}
		thinkAt   time.Time
		thinking  bool
	)

	r.emit(proto.Event{Type: proto.EventMessageStart, Turn: intp(r.turn), Role: string(message.RoleAssistant)})
	endThinking := func() {
		if !thinking {
			return
		}
		thinking = false
		ms := r.a.clock.Now().Sub(thinkAt).Milliseconds()
		r.emit(proto.Event{Type: proto.EventThinkingEnd, Turn: intp(r.turn), Ms: ms})
	}

	diag.SetOp(fmt.Sprintf("provider request turn %d", r.turn))
	resp, err := r.cfg.Provider.Stream(r.ctx, req, func(ev provider.StreamEvent) {
		switch ev.Type {
		case provider.StreamReasoningDelta:
			if !thinking {
				thinking = true
				thinkAt = r.a.clock.Now()
				r.emit(proto.Event{Type: proto.EventThinkingStart, Turn: intp(r.turn)})
			}
			reasoning.WriteString(ev.Text)
			r.emit(proto.Event{Type: proto.EventThinkingDelta, Turn: intp(r.turn), Text: ev.Text})
		case provider.StreamTextDelta:
			endThinking()
			text.WriteString(ev.Text)
			r.emit(proto.Event{Type: proto.EventTextDelta, Turn: intp(r.turn), Text: ev.Text})
		case provider.StreamToolCall:
			endThinking()
			if ev.ToolCall == nil {
				break
			}
			c := r.normalizeCall(*ev.ToolCall, len(calls), seen)
			calls = append(calls, c)
			r.emitToolCall(c)
		}
	})
	endThinking()
	if err != nil {
		return message.Message{}, provider.Usage{}, err
	}
	if resp == nil {
		return message.Message{}, provider.Usage{}, errors.New("agent: provider returned no response")
	}

	m := resp.Message
	m.Role = message.RoleAssistant
	if m.Content == "" && text.Len() > 0 {
		m.Content = text.String()
	}
	if m.Reasoning == "" && reasoning.Len() > 0 {
		m.Reasoning = reasoning.String()
	}
	if len(calls) > 0 {
		m.ToolCalls = calls
	} else if len(m.ToolCalls) > 0 {
		// A provider that assembles calls without streaming them still gets
		// normalised ids and one tool_call event per call.
		for i := range m.ToolCalls {
			m.ToolCalls[i] = r.normalizeCall(m.ToolCalls[i], i, seen)
			r.emitToolCall(m.ToolCalls[i])
		}
	}

	if h := r.cfg.Hooks.MessageEnd; h != nil {
		if out, ok := runHook(r, "message_end", func(ctx context.Context) message.Message {
			return h(ctx, m.Clone())
		}); ok {
			out.Role = message.RoleAssistant
			m = out
		}
	}

	usage := resp.Usage
	r.emit(proto.Event{Type: proto.EventMessageEnd, Turn: intp(r.turn), Message: &m, Usage: &usage})
	total := r.usage
	total.Add(usage)
	r.emit(proto.Event{Type: proto.EventUsage, Turn: intp(r.turn), Usage: &usage, Total: &total})
	return m, usage, nil
}

// normalizeCall gives every call a unique id: an empty id becomes
// call_<turn>_<index>, and a duplicate gets an _dup<n> suffix.
func (r *run) normalizeCall(c message.ToolCall, index int, seen map[string]bool) message.ToolCall {
	if c.ID == "" {
		c.ID = fmt.Sprintf("call_%d_%d", r.turn, index)
	}
	if !seen[c.ID] {
		seen[c.ID] = true
		return c
	}
	base := c.ID
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s_dup%d", base, n)
		if !seen[cand] {
			c.ID = cand
			seen[cand] = true
			return c
		}
	}
}

// cloneMessages copies a transcript, including each message's tool-call slice,
// so a hook cannot mutate the stored transcript.
func cloneMessages(msgs []message.Message) []message.Message {
	out := make([]message.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m.Clone()
	}
	return out
}
