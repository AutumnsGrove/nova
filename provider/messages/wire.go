package messages

import (
	"encoding/json"
	"strings"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// request is the Anthropic Messages request body. CacheControl is the
// automatic-caching marker: a top-level marker asks the API to place the
// breakpoint at the end of the prompt, so each turn reuses the last prefix.
type request struct {
	Model        string                 `json:"model"`
	System       string                 `json:"system,omitempty"`
	Messages     []wireMessage          `json:"messages"`
	Stream       bool                   `json:"stream"`
	MaxTokens    int                    `json:"max_tokens"`
	Tools        []toolDef              `json:"tools,omitempty"`
	ToolChoice   any                    `json:"tool_choice,omitempty"`
	Temperature  *float64               `json:"temperature,omitempty"`
	CacheControl *provider.CacheControl `json:"cache_control,omitempty"`
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
		header, payload, ok := strings.Cut(rest, ",")
		if !ok {
			return block{}, false
		}
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
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
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
			InputTokens              int `json:"input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// buildMessages converts the transcript into a system string and wire messages.
func buildMessages(messages []message.Message) (string, []wireMessage) {
	system, rest := provider.SplitSystem(messages)
	var out []wireMessage
	for _, m := range rest {
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
			blocks = append(blocks, block{
				Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: toolInput(tc.Function.Arguments),
			})
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, wireMessage{Role: string(m.Role), Content: blocks})
	}
	return system, out
}

// toolInput is the tool_use input for a call's argument string. The API wants
// a JSON object, and a model can emit arguments that are empty, cut off or not
// an object, so anything else becomes {}: embedding it raw would fail the whole
// request encode, on every retry, for as long as that call stays in the
// transcript.
func toolInput(args string) json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(args), &obj) != nil || obj == nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(args)
}

// mapToolChoice converts the request's tool choice to the Messages shape.
func mapToolChoice(choice any) any {
	switch v := choice.(type) {
	case nil:
		return nil
	case string:
		if v == "required" { // the OpenAI word for "must call some tool"
			v = "any"
		}
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

// flattenTool converts one tool definition to the Messages shape.
func flattenTool(f provider.ToolFunction) toolDef {
	return toolDef{Name: f.Name, Description: f.Description, InputSchema: f.Parameters}
}
