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

// ContentPart is one non-text piece of a message. The transcript stays
// OpenAI-shaped: the text lives in Message.Content and Parts carries media
// only, so a message with no parts serializes exactly as it did before parts
// existed. Type names the kind; "image" is the only kind v1 carries.
type ContentPart struct {
	Type     string `json:"type"`
	MimeType string `json:"mime_type,omitempty"`
	// Data is base64 bytes, with no data: prefix.
	Data string `json:"data,omitempty"`
	// URL is an alternative to Data for a caller that already holds a remote
	// image. Prefer Data: it does not depend on the provider reaching the URL.
	URL string `json:"url,omitempty"`
	// Name is for display only and is never sent to the model.
	Name string `json:"name,omitempty"`
}

// PartImage is the ContentPart type for an image.
const PartImage = "image"

// ImagePart builds an image part from a mime type and base64 data.
func ImagePart(mimeType, data string) ContentPart {
	return ContentPart{Type: PartImage, MimeType: mimeType, Data: data}
}

// Message is one entry in the transcript.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// Reasoning is the model's thinking text. It is displayed and stored but
	// not re-sent to the provider unless that provider needs it.
	Reasoning string `json:"reasoning,omitempty"`
	// ToolCalls is set on assistant messages that request tool execution.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID links a tool result back to the call that produced it.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Name is the tool name on a tool result, used by some providers.
	Name string `json:"name,omitempty"`
	// Parts carries media only, never text; Content is the text.
	Parts []ContentPart `json:"parts,omitempty"`
	// Synthetic marks a message the loop injected rather than one the user or
	// the model authored, such as the image message that follows a tool batch.
	// Clients use it to render the image in the tool card instead of as a user
	// bubble, and to avoid showing a replayed transcript as something typed.
	Synthetic bool `json:"synthetic,omitempty"`
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

// UserParts builds a user message carrying media parts. The parts are copied,
// so the caller cannot change the message by mutating its slice.
func UserParts(content string, parts ...ContentPart) Message {
	return Message{Role: RoleUser, Content: content, Parts: append([]ContentPart(nil), parts...)}
}

// WithParts returns a copy of m with parts appended. It copies the existing
// slice first, so the receiver is never mutated.
func (m Message) WithParts(parts ...ContentPart) Message {
	if len(parts) == 0 {
		return m
	}
	m.Parts = append(append([]ContentPart(nil), m.Parts...), parts...)
	return m
}

// HasImages reports whether the message carries at least one image part with
// data or a URL, which is what makes it worth sending to a vision model.
func (m Message) HasImages() bool {
	for _, p := range m.Parts {
		if p.Type == PartImage && (p.Data != "" || p.URL != "") {
			return true
		}
	}
	return false
}

// Clone returns a shallow copy with the slice fields copied, so callers can
// append to the result without mutating the original.
func (m Message) Clone() Message {
	m.ToolCalls = append([]ToolCall(nil), m.ToolCalls...)
	m.Parts = append([]ContentPart(nil), m.Parts...)
	return m
}
