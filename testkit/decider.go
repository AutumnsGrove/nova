package testkit

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/provider"
)

// DecisionTurn scripts one decision call.
type DecisionTurn struct {
	Answers map[string]decide.Answer
	Usage   provider.Usage
	Model   string
	Err     error
	Delay   time.Duration // slept via the clock before answering
	Hang    bool          // block until ctx is cancelled
}

// Decider is a scripted in-process decide.Decider. Each Decide call pops the
// next turn and records the request.
type Decider struct {
	clock Clock

	mu    sync.Mutex
	turns []DecisionTurn
	next  int
	reqs  []decide.Request
}

// NewDecider returns a decider that plays the given turns in order. A nil clock
// means the real clock.
func NewDecider(clock Clock, turns ...DecisionTurn) *Decider {
	if clock == nil {
		clock = RealClock()
	}
	return &Decider{clock: clock, turns: turns}
}

// Remaining reports how many turns are left.
func (d *Decider) Remaining() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.turns) - d.next
}

// Requests returns the recorded requests, deep-copied so callers cannot mutate
// what the decider saw.
func (d *Decider) Requests() []decide.Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]decide.Request, len(d.reqs))
	for i, r := range d.reqs {
		out[i] = cloneDecisionRequest(r)
	}
	return out
}

// Decide plays the next turn. It honours ctx for Delay and Hang, and returns an
// error once the script is exhausted.
func (d *Decider) Decide(ctx context.Context, req decide.Request) (*decide.Response, error) {
	d.mu.Lock()
	if d.next >= len(d.turns) {
		d.mu.Unlock()
		return nil, errors.New("testkit: decision script exhausted")
	}
	turn := d.turns[d.next]
	d.next++
	d.reqs = append(d.reqs, cloneDecisionRequest(req))
	d.mu.Unlock()

	if turn.Delay > 0 {
		if err := d.clock.Sleep(ctx, turn.Delay); err != nil {
			return nil, err
		}
	}
	if turn.Hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if turn.Err != nil {
		return nil, turn.Err
	}
	return &decide.Response{
		Answers: cloneAnswers(turn.Answers),
		Usage:   turn.Usage,
		Model:   turn.Model,
	}, nil
}

// cloneDecisionRequest copies the maps and source slice a caller could mutate
// after the call.
func cloneDecisionRequest(r decide.Request) decide.Request {
	out := r
	if r.Questions != nil {
		out.Questions = make(map[string]decide.Question, len(r.Questions))
		for name, q := range r.Questions {
			if q.Criteria != nil {
				q.Criteria = maps.Clone(q.Criteria)
			}
			out.Questions[name] = q
		}
	}
	if sources, ok := r.State.([]decide.Source); ok {
		out.State = append([]decide.Source(nil), sources...)
	}
	return out
}

// cloneAnswers copies the probability maps so a scripted turn can be reused.
func cloneAnswers(in map[string]decide.Answer) map[string]decide.Answer {
	if in == nil {
		return nil
	}
	out := make(map[string]decide.Answer, len(in))
	for name, ans := range in {
		if ans.Probabilities != nil {
			ans.Probabilities = maps.Clone(ans.Probabilities)
		}
		out[name] = ans
	}
	return out
}
