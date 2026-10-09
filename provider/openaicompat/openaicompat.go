// Package openaicompat implements the OpenAI chat-completions API with
// streaming and function/tool calling. It is the one wire-level provider the
// harness ships with; presets such as openrouter, ollama, and lmstudio are
// thin configurations on top of it.
package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// Config configures a Provider. BaseURL should include the API version path
// (for example "https://openrouter.ai/api/v1"); "/chat/completions" is
// appended to it.
type Config struct {
	// Name is the provider id. Defaults to "openaicompat".
	Name string
	// BaseURL is the API root, without a trailing slash.
	BaseURL string
	// APIKey is sent as "Authorization: Bearer <key>" when non-empty.
	APIKey string
	// Headers are added to every request (for example OpenRouter's
	// HTTP-Referer and X-Title).
	Headers map[string]string
	// HTTPClient overrides the default client.
	HTTPClient *http.Client
}

// Provider is an OpenAI-compatible chat-completions provider.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New builds a provider from cfg.
func New(cfg Config) *Provider {
	if cfg.Name == "" {
		cfg.Name = "openaicompat"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &Provider{cfg: cfg, client: client}
}

// Name returns the provider id.
func (p *Provider) Name() string { return p.cfg.Name }

// BaseURL returns the configured API root.
func (p *Provider) BaseURL() string { return p.cfg.BaseURL }

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatRequest struct {
	Model         string             `json:"model"`
	Messages      []message.Message  `json:"messages"`
	Tools         []provider.ToolDef `json:"tools,omitempty"`
	ToolChoice    any                `json:"tool_choice,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
	Stream        bool               `json:"stream"`
	StreamOptions *streamOptions     `json:"stream_options,omitempty"`
}

type toolCallDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type delta struct {
	Content          string          `json:"content"`
	Reasoning        string          `json:"reasoning"`
	ReasoningContent string          `json:"reasoning_content"`
	ToolCalls        []toolCallDelta `json:"tool_calls"`
}

type chunkChoice struct {
	Delta        delta  `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type rawUsage struct {
	PromptTokens        int                  `json:"prompt_tokens"`
	CompletionTokens    int                  `json:"completion_tokens"`
	TotalTokens         int                  `json:"total_tokens"`
	Cost                float64              `json:"cost"`
	PromptTokensDetails *promptTokensDetails `json:"prompt_tokens_details"`
}

type chatChunk struct {
	Choices []chunkChoice `json:"choices"`
	Usage   *rawUsage     `json:"usage"`
	Model   string        `json:"model"`
}

// Stream performs one streaming chat-completions request, accumulating text,
// reasoning, and tool-call argument deltas into a final Response.
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("openaicompat: model is required")
	}
	if p.cfg.BaseURL == "" {
		return nil, fmt.Errorf("openaicompat: base URL is required")
	}

	payload := chatRequest{
		Model:         req.Model,
		Messages:      req.Messages,
		Tools:         req.Tools,
		ToolChoice:    req.ToolChoice,
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	for k, v := range p.cfg.Headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range req.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		return nil, fmt.Errorf("openaicompat: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	result := &provider.Response{Model: req.Model}
	result.Message.Role = message.RoleAssistant

	var text strings.Builder
	type acc struct {
		id, name, typ string
		args          strings.Builder
	}
	indexed := map[int]*acc{}
	var order []int

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Ignore keep-alive comments and unknown frames rather than
			// aborting a response that is otherwise streaming fine.
			continue
		}
		if chunk.Model != "" {
			result.Model = chunk.Model
		}
		if chunk.Usage != nil {
			result.Usage = usageFromRaw(chunk.Usage)
		}

		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				result.FinishReason = choice.FinishReason
			}
			d := choice.Delta
			if d.Content != "" {
				text.WriteString(d.Content)
				if onEvent != nil {
					onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: d.Content})
				}
			}
			if reasoning := firstNonEmpty(d.Reasoning, d.ReasoningContent); reasoning != "" {
				if onEvent != nil {
					onEvent(provider.StreamEvent{Type: provider.StreamReasoningDelta, Text: reasoning})
				}
			}
			for _, tc := range d.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				a := indexed[idx]
				if a == nil {
					a = &acc{}
					indexed[idx] = a
					order = append(order, idx)
				}
				if tc.ID != "" {
					a.id = tc.ID
				}
				if tc.Type != "" {
					a.typ = tc.Type
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					a.args.WriteString(tc.Function.Arguments)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("openaicompat: read stream: %w", err)
	}

	sort.Ints(order)
	for _, idx := range order {
		a := indexed[idx]
		typ := a.typ
		if typ == "" {
			typ = "function"
		}
		id := a.id
		if id == "" {
			id = fmt.Sprintf("call_%d", idx)
		}
		tc := message.ToolCall{
			ID:       id,
			Type:     typ,
			Function: message.FunctionCall{Name: a.name, Arguments: a.args.String()},
		}
		result.Message.ToolCalls = append(result.Message.ToolCalls, tc)
		if onEvent != nil {
			done := tc
			onEvent(provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &done})
		}
	}

	result.Message.Content = text.String()
	return result, nil
}

func usageFromRaw(u *rawUsage) provider.Usage {
	out := provider.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CostUSD:          u.Cost,
	}
	if u.PromptTokensDetails != nil {
		out.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
