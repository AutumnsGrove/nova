package responses

import (
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// request is the Responses API request body. CacheControl is the
// automatic-caching marker; only a gateway that forwards to Anthropic uses it,
// and RequestCacheControl keeps it off a model that caches by itself.
type request struct {
	Model           string                 `json:"model"`
	Instructions    string                 `json:"instructions,omitempty"`
	Input           []inputItem            `json:"input"`
	Stream          bool                   `json:"stream"`
	Tools           []toolDef              `json:"tools,omitempty"`
	ToolChoice      any                    `json:"tool_choice,omitempty"`
	Temperature     *float64               `json:"temperature,omitempty"`
	MaxOutputTokens int                    `json:"max_output_tokens"`
	Reasoning       *reasoning             `json:"reasoning,omitempty"`
	CacheControl    *provider.CacheControl `json:"cache_control,omitempty"`
}

// inputItem is one input entry: a role message, a function call, or a function
// call output. Only the fields for the item's kind are set. Content is any so a
// message can send the plain string form or an array when it carries images.
type inputItem struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   any    `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

// contentItem is one entry of the array form of a message's content, used only
// when the message carries image parts.
type contentItem struct {
	Type     string `json:"type"` // "input_text" | "input_image"
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// wireContent renders a message's content as a plain string, or as an input
// array when it carries image parts. An empty message returns nil so the field
// stays omitted.
func wireContent(m message.Message) any {
	items := provider.ContentParts(m,
		func(text string) contentItem { return contentItem{Type: "input_text", Text: text} },
		func(url string) contentItem { return contentItem{Type: "input_image", ImageURL: url} })
	if len(items) > 0 {
		return items
	}
	if m.Content == "" {
		return nil
	}
	return m.Content
}

// toolDef is one flattened tool.
type toolDef struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// reasoning is the reasoning effort block.
type reasoning struct {
	Effort string `json:"effort"`
}

// event is the union of every Responses streaming event the provider reads.
type event struct {
	Type        string `json:"type"`
	Delta       string `json:"delta"`
	OutputIndex int    `json:"output_index"`
	Item        struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
	} `json:"item"`
	Response struct {
		Usage struct {
			InputTokens  int      `json:"input_tokens"`
			OutputTokens int      `json:"output_tokens"`
			TotalTokens  int      `json:"total_tokens"`
			Cost         *float64 `json:"cost"`
			InputDetails struct {
				CachedTokens     int `json:"cached_tokens"`
				CacheWriteTokens int `json:"cache_write_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		Error             errorBody `json:"error"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
	// An "error" event carries its code and message at the top level; some
	// gateways nest them under "error" as a failed response does.
	Error   errorBody `json:"error"`
	Code    any       `json:"code"`
	Message string    `json:"message"`
}

// errorBody is the error object inside a failed response.
type errorBody struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

// buildInput converts the transcript into instructions plus input items.
func buildInput(messages []message.Message) (string, []inputItem) {
	instructions, rest := provider.SplitSystem(messages)
	var items []inputItem
	for _, m := range rest {
		if m.Role == message.RoleTool {
			items = append(items, inputItem{Type: "function_call_output", CallID: m.ToolCallID, Output: m.Content})
			continue
		}
		for _, tc := range m.ToolCalls {
			items = append(items, inputItem{
				Type:      "function_call",
				CallID:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		if len(m.ToolCalls) > 0 && m.Content == "" {
			continue
		}
		items = append(items, inputItem{Role: string(m.Role), Content: wireContent(m)})
	}
	return instructions, items
}

// flattenTool converts one tool definition to the Responses shape.
func flattenTool(f provider.ToolFunction) toolDef {
	return toolDef{Type: "function", Name: f.Name, Description: f.Description, Parameters: f.Parameters}
}
