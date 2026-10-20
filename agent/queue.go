package agent

import (
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
)

// Steer queues a message injected after the current turn's tool results, and
// reports ErrNotRunning when idle. It is safe from any goroutine.
func (a *Agent) Steer(m message.Message) error {
	return a.enqueue(&a.steering, m)
}

// FollowUp queues a message run once the agent would otherwise end, and reports
// ErrNotRunning when idle. It is safe from any goroutine.
func (a *Agent) FollowUp(m message.Message) error {
	return a.enqueue(&a.followUp, m)
}

// enqueue appends to one queue and emits a queue_update. A run that has made
// its final decision refuses the message: nothing would ever consume it.
func (a *Agent) enqueue(list *[]message.Message, m message.Message) error {
	a.mu.Lock()
	if !a.running || a.closing {
		a.mu.Unlock()
		return ErrNotRunning
	}
	*list = append(*list, m)
	run := a.runID
	a.mu.Unlock()
	a.emitQueueUpdate(run)
	return nil
}

// Queued reports how many steering and follow-up messages are waiting.
func (a *Agent) Queued() (steering, followUp int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.steering), len(a.followUp)
}

// Running reports whether a run is active.
func (a *Agent) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

// Abort cancels the active run and returns the unconsumed queues' text,
// emptying them. An image on a queued message is not restored; the caller can
// ask for it again. It is harmless when idle and idempotent.
func (a *Agent) Abort() (steering, followUp []string) {
	a.mu.Lock()
	steering = contents(a.steering)
	followUp = contents(a.followUp)
	a.steering = nil
	a.followUp = nil
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return steering, followUp
}

// contents maps queued messages to their text, for the abort response.
func contents(msgs []message.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

// drainSteering removes and appends every queued steering message, in order,
// and tells clients the queue shrank so they stop showing them as waiting.
func (r *run) drainSteering() (int, error) {
	a := r.a
	a.mu.Lock()
	items := a.steering
	a.steering = nil
	a.mu.Unlock()
	if len(items) > 0 {
		a.emitQueueUpdate(r.id)
	}

	for _, m := range items {
		if err := r.record(m, nil); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// popFollowUp removes the first queued follow-up message, if any.
func (r *run) popFollowUp() (message.Message, bool) {
	a := r.a
	a.mu.Lock()
	if len(a.followUp) == 0 {
		a.mu.Unlock()
		return message.Message{}, false
	}
	m := a.followUp[0]
	a.followUp = a.followUp[1:]
	a.mu.Unlock()
	a.emitQueueUpdate(r.id)
	return m, true
}

// closeAndDrop stops the agent accepting messages and empties both queues,
// returning their text for run_end, or nil when nothing was queued.
func (a *Agent) closeAndDrop() *proto.DroppedQueue {
	a.mu.Lock()
	a.closing = true
	steering, followUp := contents(a.steering), contents(a.followUp)
	a.steering, a.followUp = nil, nil
	a.mu.Unlock()
	if len(steering) == 0 && len(followUp) == 0 {
		return nil
	}
	return &proto.DroppedQueue{Steering: steering, FollowUp: followUp}
}

// closeQueues stops the agent accepting messages, unless one arrived since the
// last drain. The emptiness check and the close are one step under the lock,
// so a Steer racing the end of the run is either seen here or refused.
func (a *Agent) closeQueues() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.steering) > 0 || len(a.followUp) > 0 {
		return false
	}
	a.closing = true
	return true
}
