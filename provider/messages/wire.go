package messages

import (
	"encoding/json"
	"strings"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// request is the Anthropic Messages request body.
type request struct {
	Model       string        `json:"model"`
	System      string        `json:"system,omitempty"`
	Messages    []wireMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	MaxTokens   int           `json:"max_tokens"`
	Tools       []toolDef     `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
}

// wireMessage is one user or assistant turn.
type wireMessage struct {
	Role    string  `json:"role"`
	Content []block `json:"content"`
}

// block is one content block. Only the fields for the block's kind are set.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	Source    *imageSource    `json:"source,omitempty"`
}

// imageSource is the Anthropic image source object. This dialect takes base64
// only here; a remote URL is not supported and is skipped.
type imageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// imageBlock converts an image part to an Anthropic image block. It accepts
// base64 Data or a data: URL, and returns false for anything else so an unknown
// or unsupported part is skipped rather than corrupting the request.
func imageBlock(p message.ContentPart) (block, bool) {
	if p.Type != message.PartImage {
		return block{}, false
	}
	data, mime := p.Data, p.MimeType
	if data == "" {
		rest, ok := strings.CutPrefix(p.URL, "data:")
		if !ok {
			return block{}, false
		}
		comma := strings.IndexByte(rest, ',')
		if comma < 0 {
			return block{}, false
		}
		header, payload := rest[:comma], rest[comma+1:]
		data = payload
		if mime == "" {
			mime, _, _ = strings.Cut(header, ";")
		}
	}
	if data == "" || mime == "" {
		return block{}, false
	}
	return block{Type: "image", Source: &imageSource{Type: "base64", MediaType: mime, Data: data}}, true
}

// toolDef is one tool.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// event is the union of every Messages streaming event the provider reads.
type event struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type     string `json:"type"`
		ID       string `json:"id"`
		Name     string `json:"name"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Message struct {
		Usage struct {
			InputTokens          int `json:"input_tokens"`
			CacheReadInputTokens int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
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

// buildMessages converts the transcript into a system string and wire messages.
func buildMessages(messages []message.Message) (string, []wireMessage) {
	system := ""
	var out []wireMessage
	for _, m := range messages {
		if m.Role == message.RoleSystem {
			if system == "" {
				system = m.Content
			} else {
				system += "\n\n" + m.Content
			}
			continue
		}
		if m.Role == message.RoleTool {
			out = append(out, wireMessage{Role: "user", Content: []block{{
				Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content,
			}}})
			continue
		}
		var blocks []block
		if m.Content != "" {
			blocks = append(blocks, block{Type: "text", Text: m.Content})
		}
		for _, p := range m.Parts {
			if b, ok := imageBlock(p); ok {
				blocks = append(blocks, b)
			}
		}
		for _, tc := range m.ToolCalls {
			args := tc.Function.Arguments
			if args == "" {
				args = "{}"
			}
			blocks = append(blocks, block{
				Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(args),
			})
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, wireMessage{Role: string(m.Role), Content: blocks})
	}
	return system, out
}

// mapToolChoice converts the request's tool choice to the Messages shape.
func mapToolChoice(choice any) any {
	switch v := choice.(type) {
	case nil:
		return nil
	case string:
		return map[string]any{"type": v}
	case map[string]any:
		if name, ok := v["name"].(string); ok {
			return map[string]any{"type": "tool", "name": name}
		}
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return map[string]any{"type": "tool", "name": name}
			}
		}
	}
	return choice
}

// flattenTools converts tool definitions to the Messages shape.
func flattenTools(tools []provider.ToolDef) []toolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]toolDef, 0, len(tools))
	for _, t := range tools {
		out = append(out, toolDef{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return out
}
