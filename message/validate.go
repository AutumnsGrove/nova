package message

import (
	"fmt"
	"maps"
	"slices"
)

// Validate checks that tool calls and their results pair up. It returns nil for
// a valid transcript, or an error naming the first offending index or call id.
//
// The rules mirror what the agent loop relies on: every assistant tool call
// gets exactly one result before the next user or assistant message, and a tool
// result never appears without a matching call in the same turn.
func Validate(msgs []Message) error {
	pending := map[string]bool{}
	for i, m := range msgs {
		switch m.Role {
		case RoleSystem:
			if i != 0 {
				return fmt.Errorf("message: system message at index %d (only index 0 is allowed)", i)
			}
		case RoleUser, RoleAssistant:
			if len(pending) > 0 {
				return fmt.Errorf("message: tool call %s has no result before index %d", firstPending(pending), i)
			}
			if m.Role == RoleAssistant {
				for _, tc := range m.ToolCalls {
					pending[tc.ID] = true
				}
			}
		case RoleTool:
			if m.ToolCallID == "" {
				return fmt.Errorf("message: tool message at index %d has no tool_call_id", i)
			}
			if !pending[m.ToolCallID] {
				return fmt.Errorf("message: tool message at index %d refers to unknown call %q", i, m.ToolCallID)
			}
			delete(pending, m.ToolCallID)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("message: tool call %s has no result", firstPending(pending))
	}
	return nil
}

// firstPending returns the smallest outstanding call id, so an error message
// does not depend on map iteration order.
func firstPending(pending map[string]bool) string {
	return slices.Min(slices.Collect(maps.Keys(pending)))
}
