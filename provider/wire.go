package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AutumnsGrove/nova/message"
)

// MergeJSON applies opts over the JSON object raw with MergeOptions and returns
// the merged object, so a ProviderOptions entry can add or replace a request
// field. Numbers are carried as written: decoding them to float64 would round
// an integer past 2^53 in a tool schema or an option.
func MergeJSON(raw []byte, opts map[string]any) ([]byte, error) {
	if len(opts) == 0 {
		return raw, nil
	}
	var base map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&base); err != nil {
		return nil, fmt.Errorf("provider: decode request for merge: %w", err)
	}
	merged, err := json.Marshal(MergeOptions(base, opts))
	if err != nil {
		return nil, fmt.Errorf("provider: encode merged request: %w", err)
	}
	return merged, nil
}

// SplitSystem lifts the system messages out of a transcript for the dialects
// that carry the system prompt outside the message list. It joins them with a
// blank line, skipping empty ones, and returns the remaining messages in order.
func SplitSystem(msgs []message.Message) (system string, rest []message.Message) {
	var prompts []string
	for _, m := range msgs {
		switch {
		case m.Role != message.RoleSystem:
			rest = append(rest, m)
		case m.Content != "":
			prompts = append(prompts, m.Content)
		}
	}
	return strings.Join(prompts, "\n\n"), rest
}

// MapTools converts tool definitions to a dialect's own tool type.
func MapTools[T any](tools []ToolDef, conv func(ToolFunction) T) []T {
	if len(tools) == 0 {
		return nil
	}
	out := make([]T, 0, len(tools))
	for _, t := range tools {
		out = append(out, conv(t.Function))
	}
	return out
}

// ImageURL returns where an image part's bytes can be fetched from: its own
// URL, or a data: URL built from its inline base64. It is empty for any other
// part kind and for an image with neither.
func ImageURL(p message.ContentPart) string {
	if p.Type != message.PartImage {
		return ""
	}
	if p.URL == "" && p.Data != "" {
		return "data:" + p.MimeType + ";base64," + p.Data
	}
	return p.URL
}

// ContentParts renders a message that carries parts as a dialect's content
// array: the text first, then one entry per usable image. It returns nil for a
// message without parts, which the caller sends as a plain string so a
// text-only request is byte-for-byte what it was before images existed. A part
// of an unknown kind, or an image with no bytes, is skipped.
func ContentParts[T any](m message.Message, text func(string) T, image func(url string) T) []T {
	if len(m.Parts) == 0 {
		return nil
	}
	out := make([]T, 0, len(m.Parts)+1)
	if m.Content != "" {
		out = append(out, text(m.Content))
	}
	for _, p := range m.Parts {
		if url := ImageURL(p); url != "" {
			out = append(out, image(url))
		}
	}
	return out
}
