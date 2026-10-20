package responses

import (
	"encoding/json"

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
	MaxOutputTokens *int                   `json:"max_output_tokens,omitempty"`
	Reasoning       *reasoning             `json:"reasoning,omitempty"`
	CacheControl    *provider.CacheControl `json:"cache_control,omitempty"`
	Extra           map[string]any         `json:"-"`
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
// stays omitted, as it was before parts existed. Text precedes the images; an
// unknown part kind or a part with no bytes or URL is skipped.
func wireContent(m message.Message) any {
	if len(m.Parts) == 0 {
		if m.Content == "" {
			return nil
		}
		return m.Content
	}
	content := make([]contentItem, 0, len(m.Parts)+1)
	if m.Content != "" {
		content = append(content, contentItem{Type: "input_text", Text: m.Content})
	}
	for _, p := range m.Parts {
		url := p.URL
		if url == "" && p.Data != "" {
			url = "data:" + p.MimeType + ";base64," + p.Data
		}
		if p.Type != message.PartImage || url == "" {
			continue
		}
		content = append(content, contentItem{Type: "input_image", ImageURL: url})
	}
	if len(content) == 0 {
		if m.Content == "" {
			return nil
		}
		return m.Content
	}
	return content
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
	ItemID      string `json:"item_id"`
	OutputIndex int    `json:"output_index"`
	Item        struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	Response struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
			InputDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// decodeEvent unmarshals one SSE payload.
func decodeEvent(data []byte) (event, error) {
	var ev event
	err := json.Unmarshal(data, &ev)
	return ev, err
}

// buildInput converts the transcript into instructions plus input items.
func buildInput(messages []message.Message) (string, []inputItem) {
	instructions := ""
	var items []inputItem
	for _, m := range messages {
		if m.Role == message.RoleSystem {
			if instructions == "" {
				instructions = m.Content
			} else {
				instructions += "\n\n" + m.Content
			}
			continue
		}
		if m.Role == message.RoleTool {
			items = append(items, inputItem{Type: "function_call_output", CallID: m.ToolCallID, Output: m.Content})
			continue
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				items = append(items, inputItem{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
			if m.Content == "" {
				continue
			}
		}
		items = append(items, inputItem{Role: string(m.Role), Content: wireContent(m)})
	}
	return instructions, items
}

// flattenTools converts tool definitions to the Responses shape.
func flattenTools(tools []provider.ToolDef) []toolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]toolDef, 0, len(tools))
	for _, t := range tools {
		out = append(out, toolDef{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	return out
}
