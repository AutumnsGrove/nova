package agent

import (
	"encoding/json"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tool"
)

// emit assigns the next sequence number and timestamp, serialises the call
// under a mutex so Seq order is Emit order, and hands the event to the
// configured consumer. It is nil-safe.
func (a *Agent) emit(ev proto.Event) {
	a.emitMu.Lock()
	defer a.emitMu.Unlock()
	a.emitLocked(ev)
}

// emitQueueUpdate reports the queue sizes as they are when the event is
// sequenced. Reading the counts under the emit lock keeps concurrent callers
// from emitting them out of order, which would leave a client showing a stale
// count after the last event.
func (a *Agent) emitQueueUpdate(runID string) {
	a.emitMu.Lock()
	defer a.emitMu.Unlock()
	a.mu.Lock()
	steering, followUp := len(a.steering), len(a.followUp)
	a.mu.Unlock()
	a.emitLocked(proto.Event{Type: proto.EventQueueUpdate, Run: runID, Steering: &steering, FollowUp: &followUp})
}

// emitLocked is emit for a caller that already holds emitMu.
func (a *Agent) emitLocked(ev proto.Event) {
	a.seq++
	ev.Seq = a.seq
	if ev.TS.IsZero() {
		ev.TS = a.clock.Now()
	}
	if a.cfg.Emit != nil {
		a.cfg.Emit(ev)
	}
}

// emit tags an event with this run's id and forwards it.
func (r *run) emit(ev proto.Event) {
	ev.Run = r.id
	r.a.emit(ev)
}

// intp returns a pointer to v, for the event fields where zero is meaningful.
func intp(v int) *int { return &v }

// wireArgs returns raw when it can travel inside a JSON frame and nil when it
// cannot. A model cut off mid-arguments leaves truncated JSON, and a RawMessage
// holding that fails to marshal, which would lose the whole event.
func wireArgs(raw json.RawMessage) json.RawMessage {
	if json.Valid(raw) {
		return raw
	}
	return nil
}

// emitToolCall reports one assembled tool call.
func (r *run) emitToolCall(c message.ToolCall) {
	args := wireArgs(json.RawMessage(c.Function.Arguments))
	r.emit(proto.Event{Type: proto.EventToolCall, Turn: intp(r.turn), Call: &proto.ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: args}})
}

// emitToolEnd reports a call's final result.
func (r *run) emitToolEnd(call message.ToolCall, res tool.Result) {
	r.emit(proto.Event{Type: proto.EventToolEnd, CallID: call.ID, Result: &proto.ToolResult{Content: res.Content, IsError: res.IsError, Truncated: res.Truncated, Details: res.Details, Parts: res.Parts}})
}

// extensionError reports a hook that panicked or exceeded its deadline.
func (r *run) extensionError(hook, message string) {
	r.emit(proto.Event{Type: proto.EventExtensionError, Hook: hook, Error: &proto.Error{Code: proto.CodeInternal, Message: message}})
}
