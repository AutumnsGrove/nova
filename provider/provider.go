// Package provider defines the model-provider contract and the registry that
// lets applications plug in new providers.
//
// Nothing in this package depends on a specific wire protocol. A provider only
// has to turn a Request into a stream of events and a final Response. The
// concrete wire dialects (provider/openaicompat, provider/responses and
// provider/messages) share the Collector, which assembles that Response from
// deltas, and the request helpers in wire.go.
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
	Model string
	// ModelID is the stable catalog id that Model (the wire slug) was derived
	// from. A router uses it to pick the provider, because a slug alone does not
	// name one; wire providers ignore it. Empty means Model is the catalog id.
	ModelID         string
	Messages        []message.Message
	Tools           []ToolDef
	ToolChoice      any
	Temperature     *float64
	MaxTokens       int
	Stop            []string
	ReasoningEffort string         // "", "low", "medium", or "high"
	ProviderOptions map[string]any // merged into the wire body by the provider
	ExtraHeaders    map[string]string
	// PromptCache overrides whether the request carries an explicit prompt
	// cache marker. Nil means decide from the catalog; see
	// RequestCacheControl. A pointer, so true and false are both meaningful.
	PromptCache *bool
}

// Usage reports token and cost accounting for one provider call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
	// CacheWriteTokens counts tokens written into a prompt cache this call.
	// A cold cache pays the write rate for the prefix it stores.
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	CostSource       string  `json:"cost_source,omitempty"` // "provider", "catalog", or ""
}

// Add accumulates another usage record into u.
func (u *Usage) Add(o Usage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.ReasoningTokens += o.ReasoningTokens
	u.TotalTokens += o.TotalTokens
	u.CachedTokens += o.CachedTokens
	u.CacheWriteTokens += o.CacheWriteTokens
	u.CostUSD += o.CostUSD
	u.CostSource = combineCostSource(u.CostSource, o.CostSource)
}

// combineCostSource keeps the strongest provenance: a provider-reported cost
// is trusted over a catalog estimate.
func combineCostSource(a, b string) string {
	if a == "provider" || b == "provider" {
		return "provider"
	}
	if a == "catalog" || b == "catalog" {
		return "catalog"
	}
	return ""
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
	// StreamUsage carries a mid-stream usage update.
	StreamUsage StreamEventType = "usage"
	// StreamError reports a mid-stream failure; the call still returns an error.
	StreamError StreamEventType = "error"
)

// StreamEvent is one chunk delivered while a response is generated.
type StreamEvent struct {
	Type     StreamEventType
	Text     string
	ToolCall *message.ToolCall
	Usage    *Usage
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
