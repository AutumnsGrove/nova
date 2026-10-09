// Package agent runs the tool-calling loop: request a completion, execute any
// tool calls, feed the results back, and repeat until the model answers or the
// turn budget runs out.
package agent

import "github.com/AutumnsGrove/nova/provider"

// EventType identifies a lifecycle event emitted during a run.
type EventType string

const (
	// EventTurnStart fires before each provider request.
	EventTurnStart EventType = "turn_start"
	// EventTextDelta carries a chunk of assistant text.
	EventTextDelta EventType = "text_delta"
	// EventReasoningDelta carries a chunk of reasoning/thinking text.
	EventReasoningDelta EventType = "reasoning_delta"
	// EventToolCall fires when the model requests a tool.
	EventToolCall EventType = "tool_call"
	// EventToolResult fires after a tool finishes (or is blocked).
	EventToolResult EventType = "tool_result"
	// EventTurnEnd fires after the assistant message and its tool results are
	// appended to the transcript.
	EventTurnEnd EventType = "turn_end"
	// EventError fires when a provider call fails.
	EventError EventType = "error"
	// EventDone fires once when the run ends normally.
	EventDone EventType = "done"
)

// Event is one observable step of a run. Consumers (CLI, TUI, tests) switch on
// Type and read the fields relevant to it.
type Event struct {
	Type EventType
	// Turn is the zero-based loop iteration.
	Turn int
	// Text is set for text/reasoning/assistant-content events.
	Text string
	// ToolCallID, ToolName, Arguments, and Result describe tool events.
	ToolCallID string
	ToolName   string
	Arguments  string
	Result     string
	IsError    bool
	// Usage is set on EventTurnEnd with the usage for that one turn.
	Usage *provider.Usage
	// Err is set on EventError.
	Err error
}
