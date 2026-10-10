package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// defaultMaxTurns bounds provider requests when Config.MaxTurns is unset.
const defaultMaxTurns = 25

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
}

// Run executes the loop over history plus prompt and returns the final result.
func (a *Agent) Run(ctx context.Context, history []message.Message, prompt message.Message) (*Result, error) {
	return a.run(ctx, history, &prompt)
}

// run is Run with an optional prompt; the compatibility function passes nil so
// history is the whole transcript.
func (a *Agent) run(ctx context.Context, history []message.Message, prompt *message.Message) (*Result, error) {
	if a.cfg.Provider == nil {
		return nil, errors.New("agent: Provider is required")
	}
	if a.cfg.Model == "" {
		return nil, errors.New("agent: Model is required")
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, ErrBusy
	}
	a.running = true
	a.runCount++
	a.runID = fmt.Sprintf("r_%d", a.runCount)
	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.running = false
		a.cancel = nil
		a.mu.Unlock()
	}()

	r := &run{a: a, cfg: a.cfg, ctx: runCtx, id: a.runID}
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

// loop runs turns until the model answers, the budget runs out, the context is
// cancelled, or the provider fails.
func (r *run) loop() (*Result, error) {
	maxTurns := r.cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	for r.turn = 0; r.turn < maxTurns; r.turn++ {
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
		continueTurn := false
		if r.cfg.Hooks.TurnEnd != nil {
			td, ok := runHook(r, "turn_end", func(ctx context.Context) TurnDecision {
				return r.cfg.Hooks.TurnEnd(ctx, Turn{Index: r.turn, Message: assistant, Results: results})
			})
			if ok {
				if td.End {
					return r.finish(r.result("done"), nil)
				}
				continueTurn = td.Continue
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
			if steered > 0 || continueTurn {
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

	res := r.result("exhausted")
	res.Exhausted = true
	res.Turns = maxTurns
	return r.finish(res, nil)
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

// finish emits the closing events, runs AgentEnd, and returns the result.
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
		runHook(r, "agent_end", func(ctx context.Context) struct{} {
			r.cfg.Hooks.AgentEnd(ctx, res)
			return struct{}{}
		})
	}
	usage := r.usage
	r.emit(proto.Event{Type: proto.EventRunEnd, Reason: res.Reason, Turn: intp(res.Turns), Usage: &usage})
	return res, err
}
