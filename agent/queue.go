package agent

import (
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
)

// Steer queues a message injected after the current turn's tool results, and
// reports ErrNotRunning when idle. It is safe from any goroutine.
func (a *Agent) Steer(text string) error {
	return a.enqueue(&a.steering, text)
}

// FollowUp queues a message run once the agent would otherwise end, and reports
// ErrNotRunning when idle. It is safe from any goroutine.
func (a *Agent) FollowUp(text string) error {
	return a.enqueue(&a.followUp, text)
}

// enqueue appends to one queue and emits a queue_update.
func (a *Agent) enqueue(list *[]string, text string) error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return ErrNotRunning
	}
	*list = append(*list, text)
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

// Abort cancels the active run and returns the unconsumed queues, emptying
// them. It is harmless when idle and idempotent.
func (a *Agent) Abort() (steering, followUp []string) {
	a.mu.Lock()
	steering = a.steering
	followUp = a.followUp
	a.steering = nil
	a.followUp = nil
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return steering, followUp
}

// drainSteering removes and appends every queued steering message, in order.
func (r *run) drainSteering() (int, error) {
	a := r.a
	a.mu.Lock()
	items := a.steering
	a.steering = nil
	a.mu.Unlock()

	for _, text := range items {
		if err := r.record(message.User(text), nil); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// popFollowUp removes the first queued follow-up message, if any.
func (r *run) popFollowUp() (string, bool) {
	a := r.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.followUp) == 0 {
		return "", false
	}
	text := a.followUp[0]
	a.followUp = a.followUp[1:]
	return text, true
}
