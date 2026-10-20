// Package messages implements the Anthropic Messages API with streaming and
// tool use. OpenCode Go serves Claude, MiniMax, and Qwen models through it.
package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/ssestream"
)

// anthropicVersion is the API version the Messages endpoint requires.
const anthropicVersion = "2023-06-01"

// defaultMaxTokens is sent when the request sets none.
const defaultMaxTokens = 4096

// Config configures a Provider. BaseURL is the API root; "/messages" is
// appended.
type Config struct {
	Name           string
	BaseURL        string
	APIKey         string
	APIKeyFunc     func(ctx context.Context) (string, error)
	Headers        map[string]string
	HTTPClient     *http.Client
	ConnectTimeout time.Duration
	IdleTimeout    time.Duration
	MaxLineBytes   int
	MaxTokens      int
}

// Provider is a Messages-API provider.
type Provider struct {
	cfg Config
}

// New builds a provider from cfg.
func New(cfg Config) *Provider {
	if cfg.Name == "" {
		cfg.Name = "messages"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokens
	}
	return &Provider{cfg: cfg}
}

// Name returns the provider id.
func (p *Provider) Name() string { return p.cfg.Name }

// Stream performs one streaming Messages request.
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Model == "" {
		return nil, fmt.Errorf("messages: model is required")
	}
	if p.cfg.BaseURL == "" {
		return nil, fmt.Errorf("messages: base URL is required")
	}
	key := p.cfg.APIKey
	if p.cfg.APIKeyFunc != nil {
		var err error
		key, err = p.cfg.APIKeyFunc(ctx)
		if err != nil {
			return nil, &provider.Error{Message: "resolving API key", Retryable: false, Err: err}
		}
	}
	body, err := p.buildBody(req)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	for k, v := range p.cfg.Headers {
		headers.Set(k, v)
	}
	for k, v := range req.ExtraHeaders {
		headers.Set(k, v)
	}
	headers.Set("anthropic-version", anthropicVersion)
	if key != "" {
		headers.Set("x-api-key", key)
	}
	s, err := ssestream.Open(ctx, ssestream.Config{
		HTTPClient:     p.cfg.HTTPClient,
		ConnectTimeout: p.cfg.ConnectTimeout,
		IdleTimeout:    p.cfg.IdleTimeout,
		MaxLineBytes:   p.cfg.MaxLineBytes,
	}, p.cfg.BaseURL+"/messages", headers, body)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return p.consume(s, req, onEvent)
}

// buildBody renders the request and merges ProviderOptions at the top level.
func (p *Provider) buildBody(req provider.Request) ([]byte, error) {
	system, msgs := buildMessages(req.Messages)
	if msgs == nil {
		msgs = []wireMessage{}
	}
	max := req.MaxTokens
	if max <= 0 {
		max = p.cfg.MaxTokens
	}
	r := request{
		Model:        req.Model,
		System:       system,
		Messages:     msgs,
		Stream:       true,
		MaxTokens:    max,
		Tools:        flattenTools(req.Tools),
		ToolChoice:   mapToolChoice(req.ToolChoice),
		CacheControl: provider.RequestCacheControl(req),
	}
	if req.Temperature != nil {
		r.Temperature = req.Temperature
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("messages: encode request: %w", err)
	}
	if len(req.ProviderOptions) == 0 {
		return data, nil
	}
	var base map[string]any
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("messages: encode request: %w", err)
	}
	return json.Marshal(provider.MergeOptions(base, req.ProviderOptions))
}

// callAcc accumulates one tool_use block.
type callAcc struct {
	id, name string
	args     strings.Builder
	index    int
}

// consume reads the event stream into a Response.
func (p *Provider) consume(s *ssestream.Stream, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	result := &provider.Response{Model: req.Model}
	result.Message.Role = message.RoleAssistant
	var text, reasoning strings.Builder
	calls := map[int]*callAcc{}
	var order []int
	stopReason := ""
	terminal := false

	partial := func() *provider.Response {
		result.Message.Content = text.String()
		result.Message.Reasoning = reasoning.String()
		return result
	}
	assembled := func() []message.ToolCall {
		indexes := append([]int(nil), order...)
		sort.Ints(indexes)
		var out []message.ToolCall
		for _, idx := range indexes {
			acc := calls[idx]
			if acc.name == "" {
				continue
			}
			args := acc.args.String()
			if args == "" {
				args = "{}"
			}
			out = append(out, message.ToolCall{
				ID:       acc.id,
				Type:     "function",
				Function: message.FunctionCall{Name: acc.name, Arguments: args},
			})
		}
		return out
	}

	for {
		raw, err := s.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return partial(), err
		}
		ev, err := decodeEvent(raw.Data)
		if err != nil {
			continue
		}
		if ev.Type == "" {
			ev.Type = raw.Name
		}
		switch ev.Type {
		case "message_start":
			result.Usage.PromptTokens = ev.Message.Usage.InputTokens
			result.Usage.CachedTokens = ev.Message.Usage.CacheReadInputTokens
			result.Usage.CacheWriteTokens = ev.Message.Usage.CacheCreationInputTokens
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				if _, ok := calls[ev.Index]; !ok {
					order = append(order, ev.Index)
				}
				calls[ev.Index] = &callAcc{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name, index: ev.Index}
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
				emit(onEvent, provider.StreamEvent{Type: provider.StreamTextDelta, Text: ev.Delta.Text})
			case "thinking_delta":
				reasoning.WriteString(ev.Delta.Thinking)
				emit(onEvent, provider.StreamEvent{Type: provider.StreamReasoningDelta, Text: ev.Delta.Thinking})
			case "input_json_delta":
				acc := calls[ev.Index]
				if acc == nil {
					acc = &callAcc{index: ev.Index}
					calls[ev.Index] = acc
					order = append(order, ev.Index)
				}
				acc.args.WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			result.Usage.CompletionTokens += ev.Usage.OutputTokens
		case "message_stop":
			terminal = true
		case "ping":
			// ignored
		case "error":
			msg := ev.Error.Message
			if msg == "" {
				msg = "messages error"
			}
			return partial(), &provider.Error{Message: msg, Retryable: false}
		}
		if terminal {
			break
		}
	}

	partial()
	complete := assembled()
	if len(complete) > 0 {
		result.Message.ToolCalls = complete
		for i := range complete {
			emit(onEvent, provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &complete[i]})
		}
	}
	finish := provider.NormalizeFinish(stopReason)
	if finish == "" && len(complete) > 0 {
		finish = "tool_calls"
	}
	result.FinishReason = finish
	if !terminal {
		return result, &provider.Error{Message: "stream ended before message_stop", Retryable: true}
	}
	return result, nil
}

// emit calls onEvent when it is set.
func emit(onEvent func(provider.StreamEvent), ev provider.StreamEvent) {
	if onEvent != nil {
		onEvent(ev)
	}
}
