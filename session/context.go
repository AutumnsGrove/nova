package session

import (
	"fmt"

	"github.com/AutumnsGrove/nova/message"
)

// Messages rebuilds the model-facing context for a branch. "" means the current
// leaf. The result always passes message.Validate; a repair is reported as a
// warning.
func (s *Store) Messages(leaf string) ([]message.Message, error) {
	msgs, _, err := s.ContextWithIDs(leaf)
	if err != nil {
		return nil, err
	}
	repaired, warnings := repairMessages(msgs)
	if len(warnings) > 0 {
		s.mu.Lock()
		for _, w := range warnings {
			s.warn(0, w)
		}
		s.mu.Unlock()
	}
	return repaired, nil
}

// ContextWithIDs is Messages before repair, with the id of the entry behind each
// message (the compaction entry for the summary). Compaction uses the ids to turn
// a cut index back into an entry, so it must see exactly the list Messages starts
// from.
func (s *Store) ContextWithIDs(leaf string) ([]message.Message, []string, error) {
	branch, err := s.Branch(leaf)
	if err != nil {
		return nil, nil, err
	}
	var msgs []message.Message
	var ids []string
	add := func(e Entry, m message.Message) {
		msgs = append(msgs, m)
		ids = append(ids, e.ID)
	}
	start := 0
	if comp := lastCompaction(branch); comp >= 0 {
		c := branch[comp]
		add(c, message.User("<summary>\n"+c.Summary+"\n</summary>"))
		start = comp + 1
		if c.FirstKept != "" {
			if idx := entryIndex(branch, c.FirstKept); idx >= 0 {
				start = idx
			}
		}
	}
	for _, e := range branch[start:] {
		if e.Type == TypeCompaction {
			continue
		}
		if m, ok := contextMessage(e); ok {
			add(e, m)
		}
	}
	return msgs, ids, nil
}

// lastCompaction returns the index of the last compaction entry, or -1.
func lastCompaction(branch []Entry) int {
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type == TypeCompaction {
			return i
		}
	}
	return -1
}

// entryIndex returns the position of id in branch, or -1.
func entryIndex(branch []Entry, id string) int {
	for i := range branch {
		if branch[i].ID == id {
			return i
		}
	}
	return -1
}

// contextMessage maps a context-bearing entry to a message.
func contextMessage(e Entry) (message.Message, bool) {
	switch e.Type {
	case TypeMessage:
		if e.Message == nil {
			return message.Message{}, false
		}
		return *e.Message, true
	case TypeCustomMessage:
		if e.InContext {
			return message.User(e.Content), true
		}
	case TypeBranchSummary:
		return message.User("<branch_summary>\n" + e.Summary + "\n</branch_summary>"), true
	}
	return message.Message{}, false
}

// repairMessages makes a transcript satisfy message.Validate: it drops a tool
// result whose call is missing and adds a placeholder for a call with no result.
func repairMessages(msgs []message.Message) ([]message.Message, []error) {
	var out []message.Message
	var warnings []error
	pending := map[string]bool{}
	var order []string
	flush := func() {
		for _, id := range order {
			if pending[id] {
				out = append(out, message.ToolResult(id, "(result unavailable)"))
				warnings = append(warnings, errNoResult(id))
				delete(pending, id)
			}
		}
		order = order[:0]
	}
	for _, m := range msgs {
		switch m.Role {
		case message.RoleAssistant:
			flush()
			for _, tc := range m.ToolCalls {
				pending[tc.ID] = true
				order = append(order, tc.ID)
			}
			out = append(out, m)
		case message.RoleTool:
			if pending[m.ToolCallID] {
				delete(pending, m.ToolCallID)
				out = append(out, m)
			} else {
				warnings = append(warnings, errOrphanResult(m.ToolCallID))
			}
		case message.RoleUser, message.RoleSystem:
			flush()
			out = append(out, m)
		default:
			out = append(out, m)
		}
	}
	flush()
	return out, warnings
}

// errNoResult reports a call repaired with a placeholder result.
func errNoResult(id string) error {
	return fmt.Errorf("tool call %s had no result; added a placeholder", id)
}

// errOrphanResult reports a tool result dropped because its call is missing.
func errOrphanResult(id string) error {
	return fmt.Errorf("dropped orphaned tool result %s", id)
}
