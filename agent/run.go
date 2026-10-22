package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// run holds the mutable state of one Agent.Run call.
type run struct {
	a        *Agent
	cfg      Config
	ctx      context.Context
	cancel   context.CancelFunc
	id       string
	turn     int
	messages []message.Message
	startLen int
	usage    provider.Usage
	answer   string
	turns    int
	ended    bool // run_end has been emitted
}

// Run executes the loop over history plus prompt and returns the final result.
func (a *Agent) Run(ctx context.Context, history []message.Message, prompt message.Message) (*Result, error) {
	return a.run(ctx, history, &prompt)
}

// Start registers a run and executes it in a background goroutine. Unlike
// calling Run in a goroutine, the run exists by the time Start returns: Running
// reports true and Abort can cancel it. That matters to a caller that answers a
// prompt and may be asked to abort straight away, because Abort is a no-op
// until a run is registered. Config errors and ErrBusy are returned here; the
// run's own outcome arrives through Emit, ending in one run_end.
func (a *Agent) Start(ctx context.Context, history []message.Message, prompt message.Message) error {
	r, err := a.begin(ctx)
	if err != nil {
		return err
	}
	go func() {
		defer r.release()
		defer diag.Catch("agent run")
		_, _ = r.execute(history, &prompt)
	}()
	return nil
}

// run is Run with an optional prompt; the compatibility function passes nil so
// history is the whole transcript.
func (a *Agent) run(ctx context.Context, history []message.Message, prompt *message.Message) (*Result, error) {
	r, err := a.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer r.release()
	return r.execute(history, prompt)
}

// begin validates the config and registers a run, so Running and Abort see it
// from this point. The caller must call the run's release when it is done.
func (a *Agent) begin(ctx context.Context) (*run, error) {
	if a.cfg.Provider == nil {
		return nil, errors.New("agent: Provider is required")
	}
	if a.cfg.Model == "" {
		return nil, errors.New("agent: Model is required")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return nil, ErrBusy
	}
	a.running = true
	a.closing = false
	a.runCount++
	a.runID = fmt.Sprintf("r_%d", a.runCount)
	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	return &run{a: a, cfg: a.cfg, ctx: runCtx, id: a.runID, cancel: cancel}, nil
}

// release ends this run's registration. It is safe to call more than once and
// never touches a newer run's registration, because finish releases the run
// before run_end is delivered and the deferred call then arrives late.
func (r *run) release() {
	r.cancel()
	a := r.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runID == r.id && a.running {
		a.running = false
		a.cancel = nil
	}
}

// execute runs the loop for a registered run. A panic in code the loop calls
// directly (the provider, a callback) ends the run as an error instead of
// crashing the process, because Start gives it no caller to recover it.
func (r *run) execute(history []message.Message, prompt *message.Message) (res *Result, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			_, _ = diag.Report(rec, "agent run")
			if !r.ended {
				res, err = r.finish(r.result("error"), fmt.Errorf("internal error: %v", rec))
			} else {
				panic(rec)
			}
		}
	}()
	diag.SetOp("run " + r.id)
	a := r.a
	if a.cfg.System != "" {
		r.messages = append(r.messages, message.System(a.cfg.System))
	}
	r.messages = append(r.messages, history...)
	r.startLen = len(r.messages)

	r.emit(proto.Event{Type: proto.EventRunStart})
	if r.ctx.Err() != nil {
		return r.finish(r.result("aborted"), r.ctx.Err())
	}
	if prompt != nil {
		if err := r.record(*prompt, nil); err != nil {
			return r.finish(r.result("error"), err)
		}
	}
	return r.loop()
}

// loop runs turns until the model answers, the context is cancelled, the
// provider fails, or an explicitly set budget runs out. With no budget the loop
// is unbounded; the abort path is what stops a runaway run.
func (r *run) loop() (*Result, error) {
	for r.turn = 0; ; r.turn++ {
		reason, err := r.runTurn()
		if reason == "" {
			continue
		}
		res := r.result(reason)
		res.Exhausted = reason == "exhausted"
		return r.finish(res, err)
	}
}

// runTurn runs one turn: request, tools, the TurnEnd hook, and the queues. It
// returns the reason the run ends, or "" when another turn should follow.
func (r *run) runTurn() (string, error) {
	// A budget is only in force when a caller set one; zero and negative both
	// mean unlimited.
	if r.cfg.MaxTurns > 0 && r.turn >= r.cfg.MaxTurns {
		if r.cfg.WrapUp != nil {
			return r.wrapUp()
		}
		return "exhausted", nil
	}
	if err := r.ctx.Err(); err != nil {
		return "aborted", err
	}
	r.emit(proto.Event{Type: proto.EventTurnStart, Turn: intp(r.turn)})

	assistant, usage, err := r.turnRequest(false)
	if err != nil {
		if cerr := r.ctx.Err(); cerr != nil {
			return "aborted", cerr
		}
		// The failed request counts as a turn, so run_end reports the work done.
		r.turns = r.turn + 1
		return "error", err
	}
	r.usage.Add(usage)
	r.turns = r.turn + 1
	if err := r.record(assistant, &usage); err != nil {
		return "error", err
	}
	if assistant.Content != "" {
		r.answer = assistant.Content
	}

	results, err := r.dispatchTools(assistant)
	if err != nil {
		return "error", err
	}
	r.emit(proto.Event{Type: proto.EventTurnEnd, Turn: intp(r.turn), Usage: &usage})
	td, err := r.turnEndHook(assistant, results)
	if err != nil {
		return "error", err
	}
	if td.End {
		return "done", nil
	}

	// Checked before the drain: a cancelled run must not move queued messages
	// into the transcript, because they are handed back to the client instead.
	if cerr := r.ctx.Err(); cerr != nil {
		return "aborted", cerr
	}
	steered, err := r.drainSteering()
	if err != nil {
		return "error", err
	}
	if len(assistant.ToolCalls) > 0 || steered > 0 || td.Continue || len(td.Inject) > 0 {
		return "", nil
	}
	more, err := r.takeQueued()
	if err != nil {
		return "error", err
	}
	if more {
		return "", nil
	}
	return "done", nil
}

// wrapUp is the final request of an exhausted run when Config.WrapUp is set.
// It appends one synthetic user instruction, offers no tools, and either ends
// with the model's answer or, when the model still tries a tool (or answers
// nothing), with the fallback. It returns the run reason: done for a real
// answer, exhausted for the fallback, error or aborted on failure.
func (r *run) wrapUp() (string, error) {
	if err := r.ctx.Err(); err != nil {
		return "aborted", err
	}
	// Copy once: the config is shared with the caller, and a run must see one
	// stable instruction and fallback even if the pointer is edited mid-turn.
	wrap := *r.cfg.WrapUp
	instruction := strings.TrimSpace(wrap.Instruction)
	if instruction == "" {
		instruction = DefaultWrapUpInstruction
	}
	fallback := strings.TrimSpace(wrap.Fallback)
	if fallback == "" {
		fallback = DefaultWrapUpFallback
	}

	r.emit(proto.Event{Type: proto.EventTurnStart, Turn: intp(r.turn)})
	wrapped := message.User(instruction)
	wrapped.Synthetic = true
	if err := r.record(wrapped, nil); err != nil {
		return "error", err
	}
	assistant, usage, err := r.turnRequest(true)
	if err != nil {
		if cerr := r.ctx.Err(); cerr != nil {
			return "aborted", cerr
		}
		r.turns = r.turn + 1
		return "error", err
	}
	r.usage.Add(usage)
	r.turns = r.turn + 1

	// A real answer ends the run normally. A tool call or an empty answer gets
	// the honest fallback: the streamed message_end already showed the model's
	// tool call, but the transcript records the fallback, because an unresolved
	// tool call would poison a later resume and an empty answer is not an answer.
	reason := "done"
	final := assistant
	if len(assistant.ToolCalls) > 0 || strings.TrimSpace(assistant.Content) == "" {
		reason = "exhausted"
		final = message.Assistant(fallback)
		final.Synthetic = true
	}
	if err := r.record(final, &usage); err != nil {
		return "error", err
	}
	r.answer = final.Content
	r.emit(proto.Event{Type: proto.EventTurnEnd, Turn: intp(r.turn), Usage: &usage})
	r.runTurnEndForWrapUp(assistant)
	return reason, nil
}

// runTurnEndForWrapUp runs the TurnEnd hook for the wrap-up turn but ignores
// its decision: a hook cannot extend a budget the caller set. The hook still
// runs so tracking and logging see the final turn.
func (r *run) runTurnEndForWrapUp(assistant message.Message) {
	h := r.cfg.Hooks.TurnEnd
	if h == nil {
		return
	}
	turn := Turn{Index: r.turn, Message: assistant.Clone()}
	_, _ = runHook(r, "turn_end", func(ctx context.Context) TurnDecision { return h(ctx, turn) })
}

// turnEndHook runs the TurnEnd hook and records the messages it injects. A hook
// that fails or times out decides nothing, so the zero decision is returned.
func (r *run) turnEndHook(assistant message.Message, results []tool.Result) (TurnDecision, error) {
	h := r.cfg.Hooks.TurnEnd
	if h == nil {
		return TurnDecision{}, nil
	}
	turn := Turn{Index: r.turn, Message: assistant.Clone(), Results: results}
	td, ok := runHook(r, "turn_end", func(ctx context.Context) TurnDecision { return h(ctx, turn) })
	if !ok || td.End {
		return TurnDecision{End: td.End}, nil
	}
	for _, m := range td.Inject {
		if err := r.record(m, nil); err != nil {
			return td, err
		}
	}
	return td, nil
}

// takeQueued appends the next queued work, steering before follow-ups, and
// reports whether there was any. It is the run's last word on ending: when it
// reports false the agent has stopped accepting messages, so nothing queued
// before that point is left behind.
func (r *run) takeQueued() (bool, error) {
	for {
		n, err := r.drainSteering()
		if err != nil || n > 0 {
			return n > 0, err
		}
		if m, ok := r.popFollowUp(); ok {
			return true, r.record(m, nil)
		}
		if r.a.closeQueues() {
			return false, nil
		}
	}
}

// dispatchTools runs the assistant message's tool calls and appends one result
// message per call, in call order. After every result it appends the messages
// the tools queued (Context.Inject) and one synthetic user message for every
// image the batch produced, because providers allow media only on user
// messages and never between a tool call and its result.
func (r *run) dispatchTools(assistant message.Message) ([]tool.Result, error) {
	if len(assistant.ToolCalls) == 0 {
		return nil, nil
	}
	outcomes := r.dispatch(assistant.ToolCalls)
	results := make([]tool.Result, len(outcomes))
	var toolUsage provider.Usage
	for i, call := range assistant.ToolCalls {
		results[i] = outcomes[i].result
		toolUsage.Add(outcomes[i].usage)
		if err := r.record(message.ToolResult(call.ID, outcomes[i].result.Content), nil); err != nil {
			return results, err
		}
	}
	if (toolUsage != provider.Usage{}) {
		r.usage.Add(toolUsage)
		total := r.usage
		r.emit(proto.Event{Type: proto.EventUsage, Turn: intp(r.turn), Usage: &toolUsage, Total: &total})
	}
	for _, o := range outcomes {
		for _, m := range o.injected {
			if err := r.record(m, nil); err != nil {
				return results, err
			}
		}
	}
	if img := toolImageMessage(outcomes); img != nil {
		if err := r.record(*img, nil); err != nil {
			return results, err
		}
	}
	return results, nil
}

// record appends a message to the working transcript and notifies OnMessage.
func (r *run) record(m message.Message, u *provider.Usage) error {
	r.messages = append(r.messages, m)
	if r.cfg.OnMessage != nil {
		return r.cfg.OnMessage(m, u)
	}
	return nil
}

// result snapshots the transcript and counters into a Result.
func (r *run) result(reason string) *Result {
	start := min(r.startLen, len(r.messages))
	newMsgs := append([]message.Message(nil), r.messages[start:]...)
	return &Result{
		Messages: append([]message.Message(nil), r.messages...),
		New:      newMsgs,
		Answer:   r.answer,
		Turns:    r.turns,
		Usage:    r.usage,
		Reason:   reason,
	}
}

// finish emits the closing events, runs AgentEnd (even after an abort), and
// returns the result.
func (r *run) finish(res *Result, err error) (*Result, error) {
	// AgentEnd can take seconds; a message queued meanwhile would never run.
	// What is still queued is dropped here and reported in run_end, so it
	// neither leaks into the next run nor vanishes.
	dropped := r.a.closeAndDrop()
	if err != nil && res.Reason == "error" {
		code := proto.CodeInternal
		var pe *provider.Error
		if errors.As(err, &pe) {
			code = proto.CodeProviderError
		}
		r.emit(proto.Event{Type: proto.EventError, Error: &proto.Error{Code: code, Message: err.Error()}})
	}
	if r.cfg.Hooks.AgentEnd != nil {
		runHookUnder(context.WithoutCancel(r.ctx), r, "agent_end", func(ctx context.Context) struct{} {
			r.cfg.Hooks.AgentEnd(ctx, res)
			return struct{}{}
		})
	}
	usage := r.usage
	r.ended = true
	r.a.emitRunEnd(r, proto.Event{Type: proto.EventRunEnd, Reason: res.Reason, Turn: intp(res.Turns), Usage: &usage, DroppedQueue: dropped})
	return res, err
}
