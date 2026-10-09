// Package provider defines the model-provider contract and the registry that
// lets applications plug in new providers.
//
// The first concrete implementation lives in provider/openaicompat (with
// provider/openrouter as a preset on top of it), but nothing in this package
// depends on a specific wire protocol. A provider only has to turn a Request
// into a stream of events and a final Response.
package provider

import (
	"context"

	"github.com/AutumnsGrove/nova/message"
)

// ToolDef is a tool offered to the model, in OpenAI function-calling shape.
type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes one callable function.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// Request is one completion request. The system prompt is carried as the
// leading system message in Messages, mirroring the model-facing transcript.
type Request struct {
	Model        string
	Messages     []message.Message
	Tools        []ToolDef
	ToolChoice   any
	Temperature  *float64
	MaxTokens    int
	ExtraHeaders map[string]string
}

// Usage reports token and cost accounting for one provider call.
type Usage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CachedTokens     int     `json:"cached_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
}

// Add accumulates another usage record into u.
func (u *Usage) Add(o Usage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.TotalTokens += o.TotalTokens
	u.CachedTokens += o.CachedTokens
	u.CostUSD += o.CostUSD
}

// Response is the finalized result of one provider call.
type Response struct {
	Message      message.Message
	FinishReason string
	Usage        Usage
	Model        string
}

// StreamEventType identifies a streaming event.
type StreamEventType string

const (
	// StreamTextDelta carries a chunk of assistant text.
	StreamTextDelta StreamEventType = "text_delta"
	// StreamReasoningDelta carries a chunk of reasoning/thinking text.
	StreamReasoningDelta StreamEventType = "reasoning_delta"
	// StreamToolCall is emitted once a tool call is fully assembled.
	StreamToolCall StreamEventType = "tool_call"
	// StreamError reports a mid-stream failure; the call still returns an error.
	StreamError StreamEventType = "error"
)

// StreamEvent is one chunk delivered while a response is generated.
type StreamEvent struct {
	Type     StreamEventType
	Text     string
	ToolCall *message.ToolCall
	Err      error
}

// Provider is a model backend.
//
// Stream must call onEvent for each incremental chunk (text, reasoning, and
// fully-assembled tool calls) and return the finalized Response. Implementations
// must honor ctx cancellation and must return a non-nil error on failure. It is
// safe for onEvent to be nil.
type Provider interface {
	// Name is the stable provider id used by the registry and model catalog.
	Name() string
	// Stream performs one completion request.
	Stream(ctx context.Context, req Request, onEvent func(StreamEvent)) (*Response, error)
}
