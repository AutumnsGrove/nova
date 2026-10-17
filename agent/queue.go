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

// enqueue appends to one queue and emits a queue_update.
func (a *Agent) enqueue(list *[]message.Message, m message.Message) error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return ErrNotRunning
	}
	*list = append(*list, m)
	steering := len(a.steering)
	followUp := len(a.followUp)
	run := a.runID
	a.mu.Unlock()
	a.emit(proto.Event{Type: proto.EventQueueUpdate, Run: run, Steering: &steering, FollowUp: &followUp})
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

// drainSteering removes and appends every queued steering message, in order.
func (r *run) drainSteering() (int, error) {
	a := r.a
	a.mu.Lock()
	items := a.steering
	a.steering = nil
	a.mu.Unlock()

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
	defer a.mu.Unlock()
	if len(a.followUp) == 0 {
		return message.Message{}, false
	}
	m := a.followUp[0]
	a.followUp = a.followUp[1:]
	return m, true
}
