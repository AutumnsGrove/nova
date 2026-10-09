// Package message defines the conversation transcript types shared by the
// agent loop, provider layer, and tools.
//
// A Message maps directly onto the OpenAI chat-completions wire format. That
// keeps the provider layer thin: there is no separate "wire message" type to
// convert to, so the transcript the loop builds is the exact payload that goes
// on the wire (minus the system prompt that the loop prepends).
package message

// Role is the speaker of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one entry in the transcript.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// ToolCalls is set on assistant messages that request tool execution.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID links a tool result back to the call that produced it.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolCall is a model-requested tool invocation.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

// FunctionCall carries the tool name and its raw JSON arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// System builds a system message.
func System(content string) Message { return Message{Role: RoleSystem, Content: content} }

// User builds a user message.
func User(content string) Message { return Message{Role: RoleUser, Content: content} }

// Assistant builds an assistant message with text content.
func Assistant(content string) Message {
	return Message{Role: RoleAssistant, Content: content}
}

// ToolResult builds a tool-result message for one tool call.
func ToolResult(toolCallID, content string) Message {
	return Message{Role: RoleTool, Content: content, ToolCallID: toolCallID}
}

// Clone returns a shallow copy with the ToolCalls slice copied, so callers can
// append to the result without mutating the original.
func (m Message) Clone() Message {
	m.ToolCalls = append([]ToolCall(nil), m.ToolCalls...)
	return m
}
