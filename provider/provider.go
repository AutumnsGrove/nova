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
	"cmp"
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
	ModelID    string
	Messages   []message.Message
	Tools      []ToolDef
	ToolChoice any
	// ParallelToolCalls asks a chat-completions provider to allow (true) or
	// forbid (false) several tool calls in one message. Nil sends nothing. It is
	// only meaningful with tools, so the wire omits it from a request that has
	// none, such as the wrap-up request. Dialects without the notion ignore it.
	ParallelToolCalls *bool
	Temperature       *float64
	MaxTokens         int
	Stop              []string
	ReasoningEffort   string         // "", "low", "medium", or "high"
	ProviderOptions   map[string]any // merged into the wire body by the provider
	ExtraHeaders      map[string]string
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
	CostSource       string  `json:"cost_source,omitempty"` // "provider", "allowance", "catalog", or ""
	// AllowanceUSD is the dollars of a subscription allowance this call
	// consumed (Go's usage.opencode.allowance). It is part of CostUSD, not
	// in addition to it: CostUSD is what the call consumed, CostSource says
	// whose dollars they were. Zero with CostSource "allowance" is a
	// reported zero (a free model), distinct from an absent allowance.
	AllowanceUSD float64 `json:"allowance_usd,omitempty"`
	// ListCostUSD is the list price of the same call
	// (Go's usage.opencode.list_cost): what it would have cost at retail.
	// It is informational only and never added to CostUSD.
	ListCostUSD float64 `json:"list_cost_usd,omitempty"`
	// BillingSource is the provider-reported billing source, such as "go",
	// "go-plus", "balance", "free", or "byok". Empty means the
	// provider did not say.
	BillingSource string `json:"billing_source,omitempty"`
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
	u.AllowanceUSD += o.AllowanceUSD
	u.ListCostUSD += o.ListCostUSD
	u.BillingSource = cmp.Or(u.BillingSource, o.BillingSource)
}

// combineCostSource keeps the strongest provenance: a provider-reported cost
// is trusted over a catalog estimate, and an allowance report counts as
// provider-reported. Between two provider-reported sources the real-money one
// ("provider") wins over the allowance one, so a mixed total reads as real
// spend; the allowance dollars stay visible in AllowanceUSD either way.
func combineCostSource(a, b string) string {
	if a == "provider" || b == "provider" {
		return "provider"
	}
	if a == "allowance" || b == "allowance" {
		return "allowance"
	}
	if a == "catalog" || b == "catalog" {
		return "catalog"
	}
	return ""
}

// OpencodeUsage is the usage.opencode object OpenCode Go attaches to every
// usage report, beside the dialect's own token counts and cost. Allowance and
// ListCost are pointers so a reported zero (a free model) reads differently
// from an absent object, the same rule the cost field already uses.
type OpencodeUsage struct {
	Allowance     *float64 `json:"allowance"`
	ListCost      *float64 `json:"list_cost"`
	BillingSource string   `json:"billing_source"`
}

// ApplyOpencode folds a present usage.opencode object into u. The allowance
// becomes the call's cost with source "allowance", because Go reports cost
// 0 for a covered call and the allowance is what the turn consumed. A nil
// object is a no-op, so dialects call this unconditionally after mapping
// their own fields.
func (u *Usage) ApplyOpencode(o *OpencodeUsage) {
	if o == nil {
		return
	}
	if o.Allowance != nil {
		u.CostUSD = *o.Allowance
		u.AllowanceUSD = *o.Allowance
	}
	u.CostSource = "allowance"
	if o.ListCost != nil {
		u.ListCostUSD = *o.ListCost
	}
	u.BillingSource = o.BillingSource
}

// Response is the finalized result of one provider call.
type Response struct {
	Message      message.Message
	FinishReason string
	Usage        Usage
	Model        string
	// Provider is the id of the provider that served the call. A plain
	// provider reports its own name; a router reports the attempt that
	// answered, so a logged call is attributable to the backend that ran it.
	Provider string
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
