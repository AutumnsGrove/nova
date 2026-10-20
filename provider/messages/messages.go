// Package messages implements the Anthropic Messages API with streaming and
// tool use. OpenCode Go serves Claude, MiniMax, and Qwen models through it.
package messages

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/ssestream"
)

// anthropicVersion is the API version the Messages endpoint requires.
const anthropicVersion = "2023-06-01"

// defaultMaxTokens is sent when the request sets none; the API requires one.
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
	cfg    Config
	target ssestream.Target
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
	target := ssestream.Target{
		Config: ssestream.Config{
			HTTPClient:     cfg.HTTPClient,
			ConnectTimeout: cfg.ConnectTimeout,
			IdleTimeout:    cfg.IdleTimeout,
			MaxLineBytes:   cfg.MaxLineBytes,
		},
		Dialect:    "messages",
		Headers:    cfg.Headers,
		Fixed:      map[string]string{"anthropic-version": anthropicVersion},
		APIKey:     cfg.APIKey,
		APIKeyFunc: cfg.APIKeyFunc,
		KeyHeader:  "x-api-key",
	}
	if cfg.BaseURL != "" {
		target.URL = cfg.BaseURL + "/messages"
	}
	return &Provider{cfg: cfg, target: target}
}

// Name returns the provider id.
func (p *Provider) Name() string { return p.cfg.Name }

// retryableErrorType lists the error types a stream can report that clear on
// their own, the same classes retryableStatus holds for an HTTP response.
var retryableErrorType = map[string]bool{"overloaded_error": true, "rate_limit_error": true, "api_error": true}

// Stream performs one streaming Messages request.
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	if err := p.target.Check(ctx, req.Model); err != nil {
		return nil, err
	}
	body, err := p.buildBody(req)
	if err != nil {
		return nil, err
	}
	s, err := p.target.Open(ctx, req.ExtraHeaders, body)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return consume(s, req.Model, onEvent)
}

// buildBody renders the request and merges ProviderOptions at the top level.
func (p *Provider) buildBody(req provider.Request) ([]byte, error) {
	system, msgs := buildMessages(req.Messages)
	if msgs == nil {
		msgs = []wireMessage{}
	}
	data, err := json.Marshal(request{
		Model:        req.Model,
		System:       system,
		Messages:     msgs,
		Stream:       true,
		MaxTokens:    cmp.Or(max(req.MaxTokens, 0), p.cfg.MaxTokens),
		Tools:        provider.MapTools(req.Tools, flattenTool),
		ToolChoice:   mapToolChoice(req.ToolChoice),
		Temperature:  req.Temperature,
		CacheControl: provider.RequestCacheControl(req),
	})
	if err != nil {
		return nil, fmt.Errorf("messages: encode request: %w", err)
	}
	return provider.MergeJSON(data, req.ProviderOptions)
}

// consume reads the event stream into a Response. A stream that ends before
// message_stop is an error, returned with the text delivered so far.
func consume(s *ssestream.Stream, model string, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	c := provider.NewCollector(model, onEvent)
	var usage provider.Usage
	stopReason := ""
	terminal := false
	// report delivers the running usage. The API sends input tokens first and
	// output tokens at the end, and never a total.
	report := func() {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		c.Usage(usage)
	}

	err := s.Each(func(raw ssestream.Event) (bool, error) {
		var ev event
		if err := raw.Decode(&ev); err != nil {
			return false, err
		}
		ev.Type = cmp.Or(ev.Type, raw.Name)
		switch ev.Type {
		case "message_start":
			// Anthropic's input_tokens is the uncached remainder; the cache
			// counts are separate. Usage.PromptTokens means the whole prompt
			// on every dialect, so sum all three here.
			u := ev.Message.Usage
			usage.PromptTokens = u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
			usage.CachedTokens = u.CacheReadInputTokens
			usage.CacheWriteTokens = u.CacheCreationInputTokens
			report()
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				c.ToolCall(ev.Index, ev.ContentBlock.ID, "", ev.ContentBlock.Name, "")
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				c.Text(ev.Delta.Text)
			case "thinking_delta":
				c.Reasoning(ev.Delta.Thinking)
			case "input_json_delta":
				c.ToolCall(ev.Index, "", "", "", ev.Delta.PartialJSON)
			}
		case "message_delta":
			stopReason = cmp.Or(ev.Delta.StopReason, stopReason)
			usage.CompletionTokens += ev.Usage.OutputTokens
			report()
		case "message_stop":
			terminal = true
		case "error":
			return false, &provider.Error{
				Code:      ev.Error.Type,
				Message:   cmp.Or(ev.Error.Message, "messages error"),
				Retryable: retryableErrorType[ev.Error.Type],
			}
		}
		return terminal, nil
	})
	switch {
	case err != nil:
		return c.Partial(), err
	case !terminal:
		return c.Partial(), &provider.Error{Message: "stream ended before message_stop", Retryable: true}
	}
	return c.Finish(stopReason), nil
}
