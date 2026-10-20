package openaicompat

import (
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// wireMessage is the chat-completions shape of a transcript message. It is
// separate from message.Message so Reasoning can be dropped without modifying
// the caller's transcript. Content is omitted when empty, which is what the
// assistants that answer with only tool calls expect.
type wireMessage struct {
	Role       message.Role       `json:"role"`
	Content    any                `json:"content,omitempty"`
	Reasoning  string             `json:"reasoning,omitempty"`
	ToolCalls  []message.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	Name       string             `json:"name,omitempty"`
}

// contentPart is one entry of the array form of a message's content, used only
// when the message carries image parts. A text-only message keeps the plain
// string form, so its bytes are unchanged.
type contentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *contentImage `json:"image_url,omitempty"`
}

// contentImage is the image_url object of an image content part.
type contentImage struct {
	URL string `json:"url"`
}

// wireContent renders a message's content as a plain string, or as a content
// array when it carries image parts. A message with neither returns nil so the
// field stays omitted, which is the shape some providers expect for an
// assistant message that only requests tools.
func wireContent(m message.Message) any {
	parts := provider.ContentParts(m,
		func(text string) contentPart { return contentPart{Type: "text", Text: text} },
		func(url string) contentPart { return contentPart{Type: "image_url", ImageURL: &contentImage{URL: url}} })
	if len(parts) > 0 {
		return parts
	}
	if m.Content == "" {
		return nil
	}
	return m.Content
}

// chatRequest is the request body. MaxTokens is a pointer so "unset" and "zero"
// are distinguishable; the API rejects zero as much as it rejects a missing
// field, but the matrix requires the key to be absent when the caller left it
// zero. CacheControl is the automatic-caching marker: it sits at the top level
// so the provider advances the breakpoint as the prompt grows.
type chatRequest struct {
	Model           string                 `json:"model"`
	Messages        []wireMessage          `json:"messages"`
	Tools           []provider.ToolDef     `json:"tools,omitempty"`
	ToolChoice      any                    `json:"tool_choice,omitempty"`
	Temperature     *float64               `json:"temperature,omitempty"`
	MaxTokens       *int                   `json:"max_tokens,omitempty"`
	Stop            []string               `json:"stop,omitempty"`
	ReasoningEffort string                 `json:"reasoning_effort,omitempty"`
	Stream          bool                   `json:"stream"`
	StreamOptions   map[string]bool        `json:"stream_options,omitempty"`
	CacheControl    *provider.CacheControl `json:"cache_control,omitempty"`
}

// toolCallDelta is one fragment of a streamed tool call. Index is a pointer
// because the field may be absent, and its zero value is a valid index.
type toolCallDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// indexOrZero is the call's index, or 0 when the provider left it out, as a
// provider that streams a single call at a time often does.
func (d toolCallDelta) indexOrZero() int {
	if d.Index == nil {
		return 0
	}
	return *d.Index
}

// reasoningDetail is one entry of the reasoning_details array some providers
// send instead of reasoning_content.
type reasoningDetail struct {
	Text string `json:"text"`
}

// delta is the choice delta. Every field is optional; null and absent both
// decode to the zero value.
type delta struct {
	Content          string            `json:"content"`
	Reasoning        string            `json:"reasoning"`
	ReasoningContent string            `json:"reasoning_content"`
	ReasoningDetails []reasoningDetail `json:"reasoning_details"`
	ToolCalls        []toolCallDelta   `json:"tool_calls"`
}

type chunkChoice struct {
	Delta        delta  `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

// tokenDetails is shared by prompt_tokens_details and
// completion_tokens_details; each parse simply ignores the other's field.
type tokenDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens"`
}

// rawUsage is the wire usage object. Cost is a pointer so an absent cost can be
// told from a reported zero, which decides CostSource.
type rawUsage struct {
	PromptTokens            int           `json:"prompt_tokens"`
	CompletionTokens        int           `json:"completion_tokens"`
	TotalTokens             int           `json:"total_tokens"`
	Cost                    *float64      `json:"cost"`
	PromptTokensDetails     *tokenDetails `json:"prompt_tokens_details"`
	CompletionTokensDetails *tokenDetails `json:"completion_tokens_details"`
}

// errorObject is the error shape returned both as an HTTP body and, on a 200,
// as the stream's first JSON object. Code is any because providers send either
// a string or a number.
type errorObject struct {
	Message string `json:"message"`
	Code    any    `json:"code"`
}

// chatChunk is one streamed chunk. Error is set when the provider reports a
// failure in-band, which it can do at any point in the stream.
type chatChunk struct {
	Error   *errorObject  `json:"error"`
	Choices []chunkChoice `json:"choices"`
	Usage   *rawUsage     `json:"usage"`
	Model   string        `json:"model"`
}

// usageFromRaw maps the wire usage onto provider.Usage. A present cost is
// trusted as provider-reported.
func usageFromRaw(u *rawUsage) provider.Usage {
	out := provider.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		out.CachedTokens = u.PromptTokensDetails.CachedTokens
		out.CacheWriteTokens = u.PromptTokensDetails.CacheWriteTokens
	}
	if u.CompletionTokensDetails != nil {
		out.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	if u.Cost != nil {
		out.CostUSD = *u.Cost
		out.CostSource = "provider"
	}
	return out
}
