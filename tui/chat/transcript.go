//go:build !notui

// Package chat folds protocol events and stored messages into transcript items
// and renders assistant text as incremental markdown. It is pure: it holds no
// connection and does no I/O.
package chat

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/image"
	"github.com/AutumnsGrove/nova/tui/list"
	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Transcript is the ordered list of items shown in the main area. Items are
// only ever appended or inserted, so their order is stable.
type Transcript struct {
	st       styles.Styles
	r        Renderer
	registry *render.Registry
	display  Display
	caps     image.Capabilities
	width    int
	now      time.Time
	items    []list.Item
	seq      int
	assists  map[string]*assistantItem // by run/turn key
	current  *assistantItem
	thinks   map[string]*thinkingItem
	tools    map[string]*toolItem // by call id
	queued   []*userItem
	kinds    map[*userItem]string
}

// NewTranscript returns an empty transcript.
func NewTranscript(width int, r Renderer, st styles.Styles) *Transcript {
	return &Transcript{
		st:       st,
		r:        r,
		registry: render.NewRegistry(),
		display:  DefaultDisplay(),
		width:    width,
		assists:  map[string]*assistantItem{},
		thinks:   map[string]*thinkingItem{},
		tools:    map[string]*toolItem{},
		kinds:    map[*userItem]string{},
	}
}

// Items returns the transcript items in order.
func (t *Transcript) Items() []list.Item { return t.items }

// SetWidth records the render width. Items pick it up on their next render.
func (t *Transcript) SetWidth(width int) { t.width = width }

// Display returns the current display preferences.
func (t *Transcript) Display() Display { return t.display }

// SetCapabilities records what the terminal can draw inline. The zero value
// means no graphics protocol, so an image renders as a labelled line.
func (t *Transcript) SetCapabilities(caps image.Capabilities) { t.caps = caps }

// SetDisplay replaces the display preferences and applies them to every item:
// it is the bulk expand/collapse behind ctrl+o and /display. A later click can
// override one item.
func (t *Transcript) SetDisplay(d Display) {
	t.display = d
	for _, th := range t.thinks {
		th.expanded = d.ExpandThinking
		th.touch()
	}
	for _, ti := range t.tools {
		ti.expanded = d.ExpandTools
		ti.touch()
	}
}

// Toggle flips the expanded state of one tool or thinking item by id. It
// reports whether the id named an expandable item.
func (t *Transcript) Toggle(id string) bool {
	for _, it := range t.items {
		switch v := it.(type) {
		case *thinkingItem:
			if v.id == id {
				v.expanded = !v.expanded
				v.touch()
				return true
			}
		case *toolItem:
			if v.id == id {
				v.expanded = !v.expanded
				v.touch()
				return true
			}
		}
	}
	return false
}

// SetRegistry replaces the tool renderer registry.
func (t *Transcript) SetRegistry(r *render.Registry) {
	if r != nil {
		t.registry = r
	}
}

// SetStyles replaces the theme styles and refreshes every item, so a theme
// switch is visible immediately.
func (t *Transcript) SetStyles(st styles.Styles) {
	t.st = st
	for _, it := range t.items {
		if rs, ok := it.(interface{ restyle(styles.Styles) }); ok {
			rs.restyle(st)
		}
	}
}

// SetNow advances the render clock. It returns the ids of items whose visible
// timer changed, so the caller can invalidate them in the list.
func (t *Transcript) SetNow(now time.Time) []string {
	t.now = now
	var changed []string
	for _, th := range t.thinks {
		if th.setNow(now) {
			changed = append(changed, th.id)
		}
	}
	for _, ti := range t.tools {
		if ti.setNow(now) {
			changed = append(changed, ti.id)
		}
	}
	return changed
}

// NeedsTick reports whether a 250 ms tick should run: a thinking block is
// streaming, or a tool call is running.
func (t *Transcript) NeedsTick() bool {
	for _, th := range t.thinks {
		if !th.done {
			return true
		}
	}
	for _, ti := range t.tools {
		if ti.call.Running {
			return true
		}
	}
	return false
}

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

// DropQueued removes every steering and follow-up message the server has not
// consumed and returns their text in order. The caller uses it when the run
// ends or aborts and the messages go back to the editor, so each one shows once.
func (t *Transcript) DropQueued() []string {
	dropped := make([]string, 0, len(t.queued))
	for _, u := range t.queued {
		dropped = append(dropped, u.text)
		t.remove(u)
		delete(t.kinds, u)
	}
	t.queued = nil
	return dropped
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
	case proto.EventThinkingStart:
		th := t.thinkingFor(e, true)
		if th != nil {
			return []string{th.id}
		}
	case proto.EventThinkingDelta:
		if th := t.thinkingFor(e, true); th != nil {
			th.text += e.Text
			th.touch()
			return []string{th.id}
		}
	case proto.EventThinkingEnd:
		if th := t.thinkingFor(e, false); th != nil {
			th.done = true
			th.ms = e.Ms
			if e.Tokens != nil {
				th.tokens = e.Tokens
			}
			th.touch()
			return []string{th.id}
		}

	case proto.EventToolCall:
		if e.Call == nil {
			return nil
		}
		id := "tool-" + e.Call.ID
		if e.Call.ID == "" {
			id = t.nextID("tool")
		}
		ti := newToolItem(t.st, t.registry, &t.display, id, e.Call.ID, e.Call, t.now)
		ti.caps = t.caps
		if e.Call.ID != "" {
			t.tools[e.Call.ID] = ti
		}
		t.append(ti)
		return []string{ti.id}
	case proto.EventToolStart:
		if ti := t.tool(e.CallID); ti != nil {
			ti.call.Running = true
			ti.call.Started = t.now
			ti.touch()
			return []string{ti.id}
		}
	case proto.EventToolUpdate:
		if ti := t.tool(e.CallID); ti != nil {
			ti.call.Live += e.Text
			ti.touch()
			return []string{ti.id}
		}
	case proto.EventToolEnd:
		if ti := t.tool(e.CallID); ti != nil {
			ti.call.Running = false
			ti.call.Ended = t.now
			if e.Result != nil {
				ti.call.Content = e.Result.Content
				ti.call.IsError = e.Result.IsError
				ti.call.Truncated = e.Result.Truncated
				ti.call.Details = e.Result.Details
				ti.parts = e.Result.Parts
			}
			ti.touch()
			return []string{ti.id}
		}

	case proto.EventMessageEnd:
		var changed []string
		if id := t.applyThinkingUsage(e); id != "" {
			changed = append(changed, id)
		}
		if a := t.currentFor(e); a != nil {
			a.done = true
			a.touch()
			changed = append(changed, a.id)
		}
		return changed

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
		if th := t.thinkingFor(e, false); th != nil {
			t.remove(th)
			delete(t.thinks, t.key(e))
			changed = append(changed, th.id)
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
		text, kind := runEndLine(e)
		if text == "" {
			return nil
		}
		li := &lineItem{base: &base{id: t.nextID("run")}, st: t.st, text: text, kind: kind}
		t.append(li)
		return []string{li.id}

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

// runEndLine returns the transcript line for a run_end reason. A normal end is
// empty, and therefore silent; every other reason, including one added later,
// gets a labelled line so a run never stops without a trace.
func runEndLine(e proto.Event) (text, kind string) {
	switch e.Reason {
	case "", "done":
		return "", ""
	case "aborted":
		return "run aborted", "muted"
	case "error":
		if e.Error != nil {
			return "run error: " + e.Error.Message, "error"
		}
		return "run error", "error"
	case "exhausted":
		if e.Turn != nil {
			return fmt.Sprintf("run stopped: turn budget reached (%d)", *e.Turn), "muted"
		}
		return "run stopped: turn budget reached", "muted"
	default:
		return "run stopped: " + e.Reason, "muted"
	}
}

// applyThinkingUsage fills a thinking item's tokens from the turn usage when
// thinking_end did not carry a count. It returns the changed id, or "".
func (t *Transcript) applyThinkingUsage(e proto.Event) string {
	th := t.thinkingFor(e, false)
	if th == nil || th.tokens != nil || e.Usage == nil || e.Usage.ReasoningTokens <= 0 {
		return ""
	}
	n := e.Usage.ReasoningTokens
	th.tokens = &n
	th.touch()
	return th.id
}

// Load rebuilds the transcript from stored messages.
func (t *Transcript) Load(msgs []message.Message) {
	t.items = nil
	t.assists = map[string]*assistantItem{}
	t.thinks = map[string]*thinkingItem{}
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
			a.done = true
			t.append(a)
			if m.Reasoning != "" {
				th := newThinkingItem(t.st, t.r, &t.display, t.nextID("thinking"), time.Time{})
				th.text = m.Reasoning
				th.done = true
				t.insertBefore(a, th)
			}
			for i := range m.ToolCalls {
				call := &m.ToolCalls[i]
				ti := newToolItem(t.st, t.registry, &t.display, "tool-"+call.ID, call.ID, &proto.ToolCall{
					ID:        call.ID,
					Name:      call.Function.Name,
					Arguments: json.RawMessage(call.Function.Arguments),
				}, time.Time{})
				t.tools[call.ID] = ti
				t.append(ti)
			}
		case message.RoleTool:
			if ti := t.tools[m.ToolCallID]; ti != nil {
				ti.call.Content = m.Content
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

// insertBefore inserts it immediately before target, or appends it when target
// is not present.
func (t *Transcript) insertBefore(target, it list.Item) {
	for i, existing := range t.items {
		if existing == target {
			t.items = append(t.items[:i], append([]list.Item{it}, t.items[i:]...)...)
			return
		}
	}
	t.append(it)
}

// remove deletes item from the transcript, preserving the order of the rest.
func (t *Transcript) remove(item list.Item) {
	for i, it := range t.items {
		if it == item {
			t.items = append(t.items[:i], t.items[i+1:]...)
			return
		}
	}
}

// key is the run/turn lookup key for a turn's items.
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

// thinkingFor returns the thinking item for an event, creating it (above the
// assistant text) when create is true and it does not exist yet.
func (t *Transcript) thinkingFor(e proto.Event, create bool) *thinkingItem {
	key := t.key(e)
	if th := t.thinks[key]; th != nil {
		return th
	}
	if !create {
		return nil
	}
	th := newThinkingItem(t.st, t.r, &t.display, t.nextID("thinking"), t.now)
	t.thinks[key] = th
	if a := t.assists[key]; a != nil {
		t.insertBefore(a, th)
	} else {
		t.append(th)
	}
	return th
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
