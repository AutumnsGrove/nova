package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"maps"
	"slices"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/tool"
)

// Tool is one tool a server advertises in tools/list.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	// The annotation hints, which the permission policy already understands.
	ReadOnly    bool
	Destructive bool
	OpenWorld   bool
}

// ToolName is how a server's tool appears in a registry. The prefix makes a
// collision with a built-in tool impossible and shows the model where the tool
// came from.
func ToolName(server, name string) string { return server + "__" + name }

// Describe prefixes a server's description with its name, so two servers that
// both offer `search` stay distinguishable in the prompt.
func Describe(server, desc string) string {
	if strings.TrimSpace(desc) == "" {
		return "[" + server + "]"
	}
	return "[" + server + "] " + desc
}

// wireTool is a tool as tools/list describes it.
type wireTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations struct {
		ReadOnly    bool `json:"readOnlyHint"`
		Destructive bool `json:"destructiveHint"`
		OpenWorld   bool `json:"openWorldHint"`
	} `json:"annotations"`
}

// ListTools lists every tool the server advertises, following nextCursor to the
// end and refusing a listing that never ends or that repeats a cursor.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opt.Timeout)
	defer cancel()
	var out []Tool
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools      []wireTool `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("mcp: %s: tools/list: %w", c.opt.Name, err)
		}
		for _, t := range res.Tools {
			if t.Name == "" {
				continue
			}
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			out = append(out, Tool{
				Name: t.Name, Description: t.Description, InputSchema: schema,
				ReadOnly: t.Annotations.ReadOnly, Destructive: t.Annotations.Destructive,
				OpenWorld: t.Annotations.OpenWorld,
			})
		}
		if res.NextCursor == "" || res.NextCursor == cursor {
			break
		}
		cursor = res.NextCursor
	}
	return out, nil
}

// wireBlock is one content block of a tools/call result.
type wireBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// resultFrom maps a tools/call result onto the tool contract: text joins into
// the content, images become Parts for the synthetic user message, and anything
// this version does not carry becomes an omission note rather than a dropped
// result. A negative content length is refused rather than allocated.
func resultFrom(raw json.RawMessage) (tool.Result, error) {
	var res struct {
		Content []wireBlock `json:"content"`
		IsError bool        `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return tool.Result{}, fmt.Errorf("mcp: tools/call result: %w", err)
	}
	var text []string
	var parts []message.ContentPart
	omitted := map[string]int{}
	for _, b := range res.Content {
		switch b.Type {
		case "text":
			text = append(text, b.Text)
		case "image":
			if b.Data == "" || b.MimeType == "" {
				omitted["image"]++
				continue
			}
			parts = append(parts, message.ImagePart(b.MimeType, b.Data))
		default:
			omitted[b.Type]++
		}
	}
	out := tool.Result{Content: strings.Join(text, "\n"), Parts: parts, IsError: res.IsError}
	if len(parts) > 0 {
		out.Caption = "images from the tool result"
	}
	for _, kind := range slices.Sorted(maps.Keys(omitted)) {
		out.Content = joinNote(out.Content, fmt.Sprintf("(%d %s block(s) omitted: this client carries text and images)", omitted[kind], kind))
	}
	if res.IsError && strings.TrimSpace(out.Content) == "" {
		out.Content = "the server reported an error with no message"
	}
	return out, nil
}

// joinNote appends a note to a result, on its own line when there is text.
func joinNote(content, note string) string {
	if strings.TrimSpace(content) == "" {
		return note
	}
	return content + "\n" + note
}
