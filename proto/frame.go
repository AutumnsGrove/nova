package proto

import (
	"encoding/json"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// Error is a protocol error with a stable code.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Attachment is one item attached to a prompt.
type Attachment struct {
	// ID names a stored upload from POST /v1/attachments. Exactly one of ID or
	// Data is set.
	ID string `json:"id,omitempty"`
	// Kind is "image" or "text"; empty with an image mime type means image.
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
	// MimeType is the media type of Data.
	MimeType string `json:"mime_type,omitempty"`
	// Data is base64 bytes, the portable form every transport can carry.
	Data string `json:"data,omitempty"`
	// Path is reserved for a local transport. A server must not read a path a
	// remote client names, so it is rejected today.
	Path string `json:"path,omitempty"`
	Text string `json:"text,omitempty"`
}

// Command asks the server to do something. Type is the command name, so an
// unknown command decodes without a change here.
type Command struct {
	Type         string       `json:"type"`
	ID           string       `json:"id,omitempty"`
	Text         string       `json:"text,omitempty"`
	Model        string       `json:"model,omitempty"`
	Level        string       `json:"level,omitempty"`
	Tokens       *int         `json:"tokens,omitempty"`
	Active       []string     `json:"active,omitempty"`
	Instructions string       `json:"instructions,omitempty"`
	Session      string       `json:"session,omitempty"`
	Entry        string       `json:"entry,omitempty"`
	Name         string       `json:"name,omitempty"`
	Args         string       `json:"args,omitempty"`
	Command      string       `json:"command,omitempty"`
	Queue        bool         `json:"queue,omitempty"`
	Attachments  []Attachment `json:"attachments,omitempty"`
}

// Response is the outcome of one command.
type Response struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Command string          `json:"command"`
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// ToolCall is one model-requested tool invocation in an event.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolResult is the outcome of one tool call in an event.
type ToolResult struct {
	Content   string          `json:"content"`
	IsError   bool            `json:"is_error"`
	Truncated bool            `json:"truncated,omitempty"`
	Details   json.RawMessage `json:"details,omitempty"`
	// Parts are images the tool showed the model, for a client to render
	// inline. They are the same parts the loop puts on the synthetic user
	// message; a client draws them once, in the tool card.
	Parts []message.ContentPart `json:"parts,omitempty"`
}

// Event is every event in one struct. Most fields are optional; a pointer is
// used where zero is a meaningful value that must survive a round trip.
type Event struct {
	Type         string           `json:"type"`
	Seq          int64            `json:"seq,omitempty"`
	TS           time.Time        `json:"ts,omitempty"`
	ID           string           `json:"id,omitempty"`
	Run          string           `json:"run,omitempty"`
	Turn         *int             `json:"turn,omitempty"`
	Role         string           `json:"role,omitempty"`
	Text         string           `json:"text,omitempty"`
	Call         *ToolCall        `json:"call,omitempty"`
	CallID       string           `json:"call_id,omitempty"`
	Result       *ToolResult      `json:"result,omitempty"`
	Message      *message.Message `json:"message,omitempty"`
	Usage        *provider.Usage  `json:"usage,omitempty"`
	Total        *provider.Usage  `json:"total,omitempty"`
	Reason       string           `json:"reason,omitempty"`
	Attempt      int              `json:"attempt,omitempty"`
	Max          int              `json:"max,omitempty"`
	DelayMS      int              `json:"delay_ms,omitempty"`
	Error        *Error           `json:"error,omitempty"`
	Entry        string           `json:"entry,omitempty"`
	Replaced     []string         `json:"replaced,omitempty"`
	TokensBefore int              `json:"tokens_before,omitempty"`
	TokensAfter  int              `json:"tokens_after,omitempty"`
	Steering     *int             `json:"steering,omitempty"`
	FollowUp     *int             `json:"follow_up,omitempty"`
	Ms           int64            `json:"ms,omitempty"`
	Tokens       *int             `json:"tokens,omitempty"`
	Extension    string           `json:"extension,omitempty"`
	Hook         string           `json:"hook,omitempty"`
	Session      string           `json:"session,omitempty"`
	Leaf         string           `json:"leaf,omitempty"`
	State        json.RawMessage  `json:"state,omitempty"`
	Decision     string           `json:"decision,omitempty"`
}

// UIOption is one choice in a select or form question.
type UIOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

// UIQuestion is one question in a form.
type UIQuestion struct {
	ID         string     `json:"id"`
	Prompt     string     `json:"prompt"`
	Kind       string     `json:"kind"` // single | multi | text
	Options    []UIOption `json:"options,omitempty"`
	Notes      bool       `json:"notes,omitempty"`
	AllowOther *bool      `json:"allow_other,omitempty"`
}

// UISegment is one segment of a status update.
type UISegment struct {
	Text  string `json:"text"`
	Style string `json:"style,omitempty"`
}

// UIRequest asks the client for input or tells it something.
type UIRequest struct {
	Type        string          `json:"type"`
	ID          string          `json:"id"`
	Method      string          `json:"method"`
	Title       string          `json:"title,omitempty"`
	Message     string          `json:"message,omitempty"`
	Default     *bool           `json:"default,omitempty"`
	TimeoutMS   *int            `json:"timeout_ms,omitempty"`
	Remember    bool            `json:"remember,omitempty"`
	Options     []UIOption      `json:"options,omitempty"`
	Multi       bool            `json:"multi,omitempty"`
	Placeholder string          `json:"placeholder,omitempty"`
	Questions   []UIQuestion    `json:"questions,omitempty"`
	AllowSkip   bool            `json:"allow_skip,omitempty"`
	Text        string          `json:"text,omitempty"`
	Level       string          `json:"level,omitempty"`
	Key         string          `json:"key,omitempty"`
	Segments    []UISegment     `json:"segments,omitempty"`
	Placement   string          `json:"placement,omitempty"`
	Content     json.RawMessage `json:"content,omitempty"`
	// Tool and Args are set on a permission confirm so the dialog can show the
	// tool and a summary of its arguments. They are empty for every other
	// method.
	Tool string          `json:"tool,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
}

// UIAnswer is the answer to one form question.
type UIAnswer struct {
	ID      string   `json:"id"`
	Value   string   `json:"value,omitempty"`
	Values  []string `json:"values,omitempty"`
	Notes   string   `json:"notes,omitempty"`
	Skipped bool     `json:"skipped,omitempty"`
}

// UIResponse is the client's answer to a UIRequest.
type UIResponse struct {
	Type      string     `json:"type"`
	ID        string     `json:"id"`
	OK        bool       `json:"ok,omitempty"`
	IDs       []string   `json:"ids,omitempty"`
	Text      string     `json:"text,omitempty"`
	Answers   []UIAnswer `json:"answers,omitempty"`
	Cancelled bool       `json:"cancelled,omitempty"`
	Remember  string     `json:"remember,omitempty"`
}

// Hello is the first frame on every connection. A client sets Client; a server
// sets Server, Session, and Modes.
type Hello struct {
	Type         string   `json:"type"`
	Version      int      `json:"version"`
	Client       string   `json:"client,omitempty"`
	Server       string   `json:"server,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Session      string   `json:"session,omitempty"`
	Modes        []string `json:"modes,omitempty"`
}

// Frame is a decoded line. Exactly one field is set.
type Frame struct {
	Command    *Command
	Response   *Response
	Event      *Event
	UIRequest  *UIRequest
	UIResponse *UIResponse
	Hello      *Hello
}
