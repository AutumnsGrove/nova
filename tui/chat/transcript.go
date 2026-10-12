//go:build !notui

// Package chat folds protocol events and stored messages into transcript items
// and renders assistant text as incremental markdown. It is pure: it holds no
// connection and does no I/O.
package chat

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/list"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Transcript is the ordered list of items shown in the main area. Items are
// only ever appended or removed, so their order is stable.
type Transcript struct {
	st      styles.Styles
	r       Renderer
	width   int
	items   []list.Item
	seq     int
	assists map[string]*assistantItem // by run/turn key
	current *assistantItem
	tools   map[string]*toolItem // by call id
	queued  []*userItem
	kinds   map[*userItem]string
}

// NewTranscript returns an empty transcript.
func NewTranscript(width int, r Renderer, st styles.Styles) *Transcript {
	return &Transcript{
		st:      st,
		r:       r,
		width:   width,
		assists: map[string]*assistantItem{},
		tools:   map[string]*toolItem{},
		kinds:   map[*userItem]string{},
	}
}

// Items returns the transcript items in order.
func (t *Transcript) Items() []list.Item { return t.items }

// SetWidth records the render width. Items pick it up on their next render.
func (t *Transcript) SetWidth(width int) { t.width = width }

// AddUser adds a user message that has already been accepted by the server.
func (t *Transcript) AddUser(text string, atts []string) string {
	u := newUserItem(t.st, t.nextID("user"), text, atts, false)
	t.append(u)
	return u.id
}

// QueueUser adds a steering or follow-up message, marked queued until the
// server consumes it. kind is "steer" or "follow_up".
func (t *Transcript) QueueUser(text, kind string) string {
	u := newUserItem(t.st, t.nextID("user"), text, nil, true)
	t.append(u)
	t.queued = append(t.queued, u)
	t.kinds[u] = kind
	return u.id
}

// Apply folds one event into the transcript and returns the ids of items that
// changed or were added.
func (t *Transcript) Apply(e proto.Event) []string {
	switch e.Type {
	case proto.EventMessageStart:
		if e.Role == string(message.RoleUser) {
			return t.consume(0, "steer")
		}
		a := newAssistantItem(t.st, t.r, t.nextID("assistant"))
		t.current = a
		t.assists[t.key(e)] = a
		t.append(a)
		return []string{a.id}

	case proto.EventTextDelta:
		if a := t.currentFor(e); a != nil {
			a.md.append(e.Text)
			a.touch()
			return []string{a.id}
		}
	case proto.EventThinkingStart, proto.EventThinkingEnd:
		if a := t.currentFor(e); a != nil {
			a.touch()
			return []string{a.id}
		}
	case proto.EventThinkingDelta:
		if a := t.currentFor(e); a != nil {
			a.thinking += e.Text
			a.touch()
			return []string{a.id}
		}

	case proto.EventToolCall:
		if e.Call == nil {
			return nil
		}
		id := "tool-" + e.Call.ID
		if e.Call.ID == "" {
			id = t.nextID("tool")
		}
		ti := newToolItem(t.st, id, e.Call.ID, e.Call)
		if e.Call.ID != "" {
			t.tools[e.Call.ID] = ti
		}
		t.append(ti)
		return []string{ti.id}
	case proto.EventToolStart:
		if ti := t.tool(e.CallID); ti != nil {
			ti.status = "running"
			ti.touch()
			return []string{ti.id}
		}
	case proto.EventToolUpdate:
		if ti := t.tool(e.CallID); ti != nil {
			ti.output += e.Text
			ti.touch()
			return []string{ti.id}
		}
	case proto.EventToolEnd:
		if ti := t.tool(e.CallID); ti != nil {
			if e.Result != nil {
				ti.result = e.Result.Content
				ti.isError = e.Result.IsError
			}
			ti.status = "done"
			if ti.isError {
				ti.status = "error"
			}
			ti.touch()
			return []string{ti.id}
		}

	case proto.EventMessageEnd:
		if a := t.currentFor(e); a != nil {
			a.done = true
			a.touch()
			return []string{a.id}
		}

	case proto.EventCompaction:
		ci := &compactionItem{base: &base{id: t.nextID("compaction")}, st: t.st, before: e.TokensBefore, after: e.TokensAfter}
		t.append(ci)
		return []string{ci.id}

	case proto.EventError:
		li := &lineItem{base: &base{id: t.nextID("error")}, st: t.st, text: e.Text, kind: "error"}
		if e.Error != nil {
			li.text = e.Error.Message
		}
		t.append(li)
		return []string{li.id}

	case proto.EventRetry:
		var changed []string
		if a := t.currentFor(e); a != nil {
			t.remove(a)
			changed = append(changed, a.id)
			t.current = nil
		}
		li := &lineItem{
			base: &base{id: t.nextID("retry")},
			st:   t.st,
			text: fmt.Sprintf("retrying (%d/%d)", e.Attempt, e.Max),
			kind: "muted",
		}
		t.append(li)
		changed = append(changed, li.id)
		return changed

	case proto.EventRunEnd:
		if e.Reason == "aborted" || e.Reason == "error" {
			text, kind := "run aborted", "muted"
			if e.Reason == "error" {
				text, kind = "run error", "error"
				if e.Error != nil {
					text = "run error: " + e.Error.Message
				}
			}
			li := &lineItem{base: &base{id: t.nextID("run")}, st: t.st, text: text, kind: kind}
			t.append(li)
			return []string{li.id}
		}

	case proto.EventQueueUpdate:
		var changed []string
		if e.Steering != nil {
			changed = append(changed, t.consume(*e.Steering, "steer")...)
		}
		if e.FollowUp != nil {
			changed = append(changed, t.consume(*e.FollowUp, "follow_up")...)
		}
		return changed
	}
	return nil
}

// Load rebuilds the transcript from stored messages.
func (t *Transcript) Load(msgs []message.Message) {
	t.items = nil
	t.assists = map[string]*assistantItem{}
	t.tools = map[string]*toolItem{}
	t.queued = nil
	t.kinds = map[*userItem]string{}
	t.current = nil
	t.seq = 0
	for _, m := range msgs {
		switch m.Role {
		case message.RoleUser:
			t.AddUser(m.Content, nil)
		case message.RoleAssistant:
			a := newAssistantItem(t.st, t.r, t.nextID("assistant"))
			if m.Content != "" {
				a.md.append(m.Content)
			}
			a.thinking = m.Reasoning
			a.done = true
			t.append(a)
			for i := range m.ToolCalls {
				call := &m.ToolCalls[i]
				ti := newToolItem(t.st, "tool-"+call.ID, call.ID, &proto.ToolCall{
					ID:        call.ID,
					Name:      call.Function.Name,
					Arguments: json.RawMessage(call.Function.Arguments),
				})
				t.tools[call.ID] = ti
				t.append(ti)
			}
		case message.RoleTool:
			if ti := t.tools[m.ToolCallID]; ti != nil {
				ti.result = m.Content
				ti.status = "done"
				ti.touch()
			} else {
				li := &lineItem{base: &base{id: t.nextID("note")}, st: t.st, text: "tool result: " + m.Content, kind: "muted"}
				t.append(li)
			}
		}
	}
}

// nextID returns a fresh stable id with the given prefix.
func (t *Transcript) nextID(prefix string) string {
	t.seq++
	return prefix + "-" + strconv.Itoa(t.seq)
}

// append adds an item to the end.
func (t *Transcript) append(it list.Item) { t.items = append(t.items, it) }

// remove deletes item from the transcript, preserving the order of the rest.
func (t *Transcript) remove(item list.Item) {
	for i, it := range t.items {
		if it == item {
			t.items = append(t.items[:i], t.items[i+1:]...)
			return
		}
	}
}

// key is the run/turn lookup key for an assistant item.
func (t *Transcript) key(e proto.Event) string {
	turn := 0
	if e.Turn != nil {
		turn = *e.Turn
	}
	return e.Run + "/" + strconv.Itoa(turn)
}

// currentFor returns the assistant item an event belongs to.
func (t *Transcript) currentFor(e proto.Event) *assistantItem {
	if e.Turn != nil {
		if a := t.assists[t.key(e)]; a != nil {
			return a
		}
	}
	return t.current
}

// tool returns a tool item by call id, dropping unknown ids.
func (t *Transcript) tool(callID string) *toolItem {
	if callID == "" {
		return nil
	}
	return t.tools[callID]
}

// consume marks queued messages of a kind normal until the count matches.
func (t *Transcript) consume(count int, kind string) []string {
	var changed []string
	for t.countQueued(kind) > count {
		i := t.firstQueued(kind)
		if i < 0 {
			break
		}
		u := t.queued[i]
		u.queued = false
		u.touch()
		t.queued = append(t.queued[:i], t.queued[i+1:]...)
		delete(t.kinds, u)
		changed = append(changed, u.id)
	}
	return changed
}

// countQueued counts queued messages of a kind.
func (t *Transcript) countQueued(kind string) int {
	n := 0
	for _, u := range t.queued {
		if t.kinds[u] == kind {
			n++
		}
	}
	return n
}

// firstQueued is the index of the oldest queued message of a kind, or -1.
func (t *Transcript) firstQueued(kind string) int {
	for i, u := range t.queued {
		if t.kinds[u] == kind {
			return i
		}
	}
	return -1
}
