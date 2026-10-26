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
// that response's usage. noTools strips every tool after the hooks too, so a
// wrap-up request can never offer one.
func (r *run) turnRequest(noTools bool) (message.Message, provider.Usage, error) {
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
		Model: r.cfg.Model, ModelID: r.cfg.ModelID,
		Messages:        r.messages,
		Temperature:     r.cfg.Temperature,
		MaxTokens:       r.cfg.MaxTokens,
		ReasoningEffort: r.cfg.ReasoningEffort,
		ProviderOptions: r.cfg.ProviderOptions,
		PromptCache:     r.cfg.PromptCache,
	}
	if r.cfg.Tools != nil && !noTools {
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
	req.Messages = withoutEmptyAssistant(req.Messages)
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
	if noTools {
		req.Tools = nil
		req.ToolChoice = nil
	}
	return r.callProvider(r.gateImages(req))
}

// withoutEmptyAssistant drops assistant messages that carry no content, no
// parts and no tool calls. A model that returns only reasoning leaves one in the
// transcript, and a provider can reject it (an empty text block, or neither
// content nor tool_calls). The transcript keeps it, because the caller owns
// that; only the request omits it. It runs after TransformContext, so that hook
// still sees the message. The input slice is never modified.
func withoutEmptyAssistant(msgs []message.Message) []message.Message {
	empty := func(m message.Message) bool {
		return m.Role == message.RoleAssistant && m.Content == "" && len(m.Parts) == 0 && len(m.ToolCalls) == 0
	}
	n := 0
	for _, m := range msgs {
		if empty(m) {
			n++
		}
	}
	if n == 0 {
		return msgs
	}
	out := make([]message.Message, 0, len(msgs)-n)
	for _, m := range msgs {
		if !empty(m) {
			out = append(out, m)
		}
	}
	return out
}

// callProvider performs the request and retries a retryable provider error,
// emitting a retry event before each wait. Cancellation is never retried.
func (r *run) callProvider(req provider.Request) (message.Message, provider.Usage, error) {
	maxRetries := r.cfg.Retry.Max
	switch {
	case maxRetries < 0:
		maxRetries = 0
	case maxRetries == 0:
		maxRetries = defaultRetryMax
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
		if attempt >= maxRetries || !errors.As(err, &pe) || !pe.Retryable {
			return message.Message{}, provider.Usage{}, err
		}
		delay := retryDelay(r.cfg.Retry, pe, attempt)
		r.emit(proto.Event{Type: proto.EventRetry, Attempt: attempt + 1, Max: maxRetries, DelayMS: int(delay.Milliseconds()), Error: &proto.Error{Code: proto.CodeProviderError, Message: err.Error()}})
		if serr := r.a.clock.Sleep(r.ctx, delay); serr != nil {
			return message.Message{}, provider.Usage{}, serr
		}
	}
}

// streamOnce performs one provider request and emits every per-attempt event:
// message_start, thinking/text deltas, tool_call, message_end, and usage.
func (r *run) streamOnce(req provider.Request) (message.Message, provider.Usage, error) {
	st := &attempt{r: r, seen: map[string]bool{}}
	r.emit(proto.Event{Type: proto.EventMessageStart, Turn: intp(r.turn), Role: string(message.RoleAssistant)})
	st.textGate.filter = r.newStreamFilter(r.cfg.TextFilter, "text_filter")
	st.reasoningGate.filter = r.newStreamFilter(r.cfg.ReasoningFilter, "reasoning_filter")

	diag.SetOp(fmt.Sprintf("provider request turn %d", r.turn))
	ctx := r.ctx
	if r.cfg.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.RequestTimeout)
		defer cancel()
	}
	resp, err := r.cfg.Provider.Stream(ctx, req, st.onEvent)
	if err != nil {
		st.endThinking()
		return message.Message{}, provider.Usage{}, err
	}
	if resp == nil {
		st.endThinking()
		return message.Message{}, provider.Usage{}, errors.New("agent: provider returned no response")
	}
	st.flushFilters()
	st.endThinking()

	m := st.message(resp.Message)
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

// attempt collects one provider attempt's stream: it forwards each delta as an
// event and keeps what the final message may need when the provider's response
// carries less than was streamed.
type attempt struct {
	r             *run
	text          strings.Builder
	reasoning     strings.Builder
	calls         []message.ToolCall
	seen          map[string]bool
	thinkAt       time.Time
	thinking      bool
	textGate      streamGate
	reasoningGate streamGate
}

// streamGate is one kind's filter plus the text the StreamDelta hook is holding.
// The filter owns whatever it holds internally; the gate owns only the hook's
// buffer, so a filter and a hook can both hold without sharing state.
type streamGate struct {
	filter StreamFilter
	held   strings.Builder
}

// releaseHeld returns and clears the hook's held text.
func (g *streamGate) releaseHeld() string {
	if g.held.Len() == 0 {
		return ""
	}
	out := g.held.String()
	g.held.Reset()
	return out
}

// onEvent handles one stream event. Reasoning opens a thinking span; the first
// text or tool call closes it.
func (st *attempt) onEvent(ev provider.StreamEvent) {
	r := st.r
	switch ev.Type {
	case provider.StreamReasoningDelta:
		st.reasoning.WriteString(ev.Text)
		if shown := st.show(&st.reasoningGate, StreamReasoning, "reasoning_filter", ev.Text); shown != "" {
			st.beginThinking()
			r.emit(proto.Event{Type: proto.EventThinkingDelta, Turn: intp(r.turn), Text: shown})
		}
	case provider.StreamTextDelta:
		st.text.WriteString(ev.Text)
		if shown := st.show(&st.textGate, StreamText, "text_filter", ev.Text); shown != "" {
			st.endThinking()
			r.emit(proto.Event{Type: proto.EventTextDelta, Turn: intp(r.turn), Text: shown})
		}
	case provider.StreamToolCall:
		st.endThinking()
		if ev.ToolCall == nil {
			return
		}
		c := r.normalizeCall(*ev.ToolCall, len(st.calls), st.seen)
		st.calls = append(st.calls, c)
		r.emitToolCall(c)
	}
}

// show runs one delta through the gate's filter and then the StreamDelta hook.
// It returns the text to emit now, or "" when either layer is holding it.
func (st *attempt) show(gate *streamGate, kind StreamKind, filterName, text string) string {
	shown := st.filter(gate.filter, filterName, text)
	if shown == "" {
		return ""
	}
	return st.applyHook(gate, kind, shown)
}

// applyHook runs the StreamDelta hook for one filtered chunk. A hook that
// panics or exceeds its deadline releases what it held and lets the text
// through, so a broken hook cannot lose the answer or stall the stream.
func (st *attempt) applyHook(gate *streamGate, kind StreamKind, text string) string {
	h := st.r.cfg.Hooks.StreamDelta
	if h == nil {
		return text
	}
	ev := StreamDelta{Kind: kind, Text: text}
	decision, ok := runHook(st.r, "stream_delta", func(ctx context.Context) StreamDecision { return h(ctx, ev) })
	if !ok {
		return gate.releaseHeld() + text
	}
	switch decision.Action {
	case DeltaHold:
		gate.held.WriteString(text)
		return ""
	case DeltaDrop:
		gate.held.Reset()
		return ""
	default: // DeltaPass, and any action a hand-built decision mistyped.
		held := gate.releaseHeld()
		if decision.Text != "" {
			return held + decision.Text
		}
		return held + text
	}
}

// newStreamFilter builds one attempt's filter. A panicking factory is reported
// and treated as no filter, so the stream still reaches the client.
func (r *run) newStreamFilter(factory func() StreamFilter, name string) (f StreamFilter) {
	if factory == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			r.extensionError(name, "filter factory panicked")
			f = nil
		}
	}()
	return factory()
}

// filter runs one delta through the filter. A panic is reported and the original
// text passes through, so a broken filter cannot lose the answer or crash the
// loop.
func (st *attempt) filter(f StreamFilter, name, text string) (shown string) {
	if f == nil {
		return text
	}
	defer func() {
		if recover() != nil {
			st.r.extensionError(name, "filter panicked")
			shown = text
		}
	}()
	return f.Delta(st.r.ctx, text)
}

// flushFilter releases whatever a filter held at the end of a successful
// message. A panic is reported and the held text is dropped; the stream still
// ends cleanly.
func (st *attempt) flushFilter(f StreamFilter, name string) (shown string) {
	if f == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			st.r.extensionError(name, "filter panicked")
			shown = ""
		}
	}()
	return f.Flush(st.r.ctx)
}

// flushGate releases a filter's held text through the hook, then releases
// anything the hook itself held. The hook is applied to the filter's flush so
// the composition stays filter-then-hook even at the end of a message.
func (st *attempt) flushGate(gate *streamGate, kind StreamKind, filterName string) string {
	var out strings.Builder
	if held := st.flushFilter(gate.filter, filterName); held != "" {
		out.WriteString(st.applyHook(gate, kind, held))
	}
	out.WriteString(gate.releaseHeld())
	return out.String()
}

// flushFilters releases held reasoning then held text, in that order, so a
// thinking span opened by a late reasoning flush is closed by a late text
// flush.
func (st *attempt) flushFilters() {
	if held := st.flushGate(&st.reasoningGate, StreamReasoning, "reasoning_filter"); held != "" {
		st.beginThinking()
		st.r.emit(proto.Event{Type: proto.EventThinkingDelta, Turn: intp(st.r.turn), Text: held})
	}
	if held := st.flushGate(&st.textGate, StreamText, "text_filter"); held != "" {
		st.endThinking()
		st.r.emit(proto.Event{Type: proto.EventTextDelta, Turn: intp(st.r.turn), Text: held})
	}
}

// beginThinking opens a thinking span at most once.
func (st *attempt) beginThinking() {
	if st.thinking {
		return
	}
	st.thinking = true
	st.thinkAt = st.r.a.clock.Now()
	st.r.emit(proto.Event{Type: proto.EventThinkingStart, Turn: intp(st.r.turn)})
}

// endThinking closes an open thinking span with its duration from the clock.
func (st *attempt) endThinking() {
	if !st.thinking {
		return
	}
	st.thinking = false
	ms := st.r.a.clock.Now().Sub(st.thinkAt).Milliseconds()
	st.r.emit(proto.Event{Type: proto.EventThinkingEnd, Turn: intp(st.r.turn), Ms: ms})
}

// message builds the assistant message from the provider's response and what
// streamed. Streamed text and reasoning fill in for empty fields, and the
// streamed calls win because they already carry normalised ids.
func (st *attempt) message(m message.Message) message.Message {
	m.Role = message.RoleAssistant
	if m.Content == "" && st.text.Len() > 0 {
		m.Content = st.text.String()
	}
	if m.Reasoning == "" && st.reasoning.Len() > 0 {
		m.Reasoning = st.reasoning.String()
	}
	if len(st.calls) > 0 {
		m.ToolCalls = st.calls
	} else {
		// A provider that assembles calls without streaming them still gets
		// normalised ids and one tool_call event per call.
		for i := range m.ToolCalls {
			m.ToolCalls[i] = st.r.normalizeCall(m.ToolCalls[i], i, st.seen)
			st.r.emitToolCall(m.ToolCalls[i])
		}
	}
	return m
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
