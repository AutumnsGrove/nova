package agent

import (
	"context"
	"errors"
	"fmt"

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
	r, release, err := a.begin(ctx)
	if err != nil {
		return err
	}
	go func() {
		defer release()
		defer diag.Catch("agent run")
		_, _ = r.execute(history, &prompt)
	}()
	return nil
}

// run is Run with an optional prompt; the compatibility function passes nil so
// history is the whole transcript.
func (a *Agent) run(ctx context.Context, history []message.Message, prompt *message.Message) (*Result, error) {
	r, release, err := a.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return r.execute(history, prompt)
}

// begin validates the config and registers a run, so Running and Abort see it
// from this point. The returned release ends the registration and must be
// called exactly once, after the run finishes.
func (a *Agent) begin(ctx context.Context) (*run, func(), error) {
	if a.cfg.Provider == nil {
		return nil, nil, errors.New("agent: Provider is required")
	}
	if a.cfg.Model == "" {
		return nil, nil, errors.New("agent: Model is required")
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, nil, ErrBusy
	}
	a.running = true
	a.runCount++
	a.runID = fmt.Sprintf("r_%d", a.runCount)
	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()
	release := func() {
		cancel()
		a.mu.Lock()
		a.running = false
		a.cancel = nil
		a.mu.Unlock()
	}
	return &run{a: a, cfg: a.cfg, ctx: runCtx, id: a.runID}, release, nil
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
	maxTurns := r.cfg.MaxTurns
	for r.turn = 0; ; r.turn++ {
		// A budget is only in force when a caller set one; zero and negative
		// both mean unlimited.
		if maxTurns > 0 && r.turn >= maxTurns {
			res := r.result("exhausted")
			res.Exhausted = true
			res.Turns = maxTurns
			return r.finish(res, nil)
		}
		if err := r.ctx.Err(); err != nil {
			return r.finish(r.result("aborted"), err)
		}
		r.emit(proto.Event{Type: proto.EventTurnStart, Turn: intp(r.turn)})

		assistant, usage, err := r.turnRequest()
		if err != nil {
			if cerr := r.ctx.Err(); cerr != nil {
				return r.finish(r.result("aborted"), cerr)
			}
			res := r.result("error")
			res.Turns = r.turn + 1
			return r.finish(res, err)
		}
		r.usage.Add(usage)
		r.turns = r.turn + 1
		if err := r.record(assistant, &usage); err != nil {
			return r.finish(r.result("error"), err)
		}
		if assistant.Content != "" {
			r.answer = assistant.Content
		}

		results, err := r.dispatchTools(assistant)
		if err != nil {
			return r.finish(r.result("error"), err)
		}
		r.emit(proto.Event{Type: proto.EventTurnEnd, Turn: intp(r.turn), Usage: &usage})
		continueTurn := false
		injected := 0
		if r.cfg.Hooks.TurnEnd != nil {
			turnIndex := r.turn
			td, ok := runHook(r, "turn_end", func(ctx context.Context) TurnDecision {
				return r.cfg.Hooks.TurnEnd(ctx, Turn{Index: turnIndex, Message: assistant, Results: results})
			})
			if ok {
				if td.End {
					return r.finish(r.result("done"), nil)
				}
				continueTurn = td.Continue
				injected = len(td.Inject)
				for _, m := range td.Inject {
					if err := r.record(m, nil); err != nil {
						return r.finish(r.result("error"), err)
					}
				}
			}
		}

		steered, err := r.drainSteering()
		if err != nil {
			return r.finish(r.result("error"), err)
		}
		if cerr := r.ctx.Err(); cerr != nil {
			return r.finish(r.result("aborted"), cerr)
		}
		if len(assistant.ToolCalls) == 0 {
			if steered > 0 || continueTurn || injected > 0 {
				continue
			}
			if text, ok := r.popFollowUp(); ok {
				if err := r.record(message.User(text), nil); err != nil {
					return r.finish(r.result("error"), err)
				}
				continue
			}
			return r.finish(r.result("done"), nil)
		}
	}
}

// dispatchTools runs the assistant message's tool calls and appends one result
// message per call, in call order.
func (r *run) dispatchTools(assistant message.Message) ([]tool.Result, error) {
	if len(assistant.ToolCalls) == 0 {
		return nil, nil
	}
	results := r.dispatch(assistant.ToolCalls)
	for i, call := range assistant.ToolCalls {
		content := results[i].Content
		if content == "" {
			content = "(empty result)"
		}
		if err := r.record(message.ToolResult(call.ID, content), nil); err != nil {
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
	if err != nil && res.Reason == "error" {
		code := proto.CodeInternal
		var pe *provider.Error
		if errors.As(err, &pe) {
			code = proto.CodeProviderError
		}
		r.emit(proto.Event{Type: proto.EventError, Error: &proto.Error{Code: code, Message: err.Error()}})
	}
	if r.cfg.Hooks.AgentEnd != nil {
		runHookUnder(r, context.WithoutCancel(r.ctx), "agent_end", func(ctx context.Context) struct{} {
			r.cfg.Hooks.AgentEnd(ctx, res)
			return struct{}{}
		})
	}
	usage := r.usage
	r.ended = true
	r.emit(proto.Event{Type: proto.EventRunEnd, Reason: res.Reason, Turn: intp(res.Turns), Usage: &usage})
	return res, err
}
