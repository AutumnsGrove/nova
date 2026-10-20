// Package responses implements the OpenAI Responses API with streaming and
// function calling. OpenCode Go serves its GPT and Grok models through it.
package responses

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

// defaultMaxOutputTokens is sent when neither the request nor the config sets
// a limit; the API requires one.
const defaultMaxOutputTokens = 4096

// Config configures a Provider. BaseURL is the API root; "/responses" is
// appended.
type Config struct {
	Name            string
	BaseURL         string
	APIKey          string
	APIKeyFunc      func(ctx context.Context) (string, error)
	Headers         map[string]string
	HTTPClient      *http.Client
	ConnectTimeout  time.Duration
	IdleTimeout     time.Duration
	MaxLineBytes    int
	MaxOutputTokens int
}

// Provider is a Responses-API provider.
type Provider struct {
	cfg    Config
	target ssestream.Target
}

// New builds a provider from cfg.
func New(cfg Config) *Provider {
	if cfg.Name == "" {
		cfg.Name = "responses"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = defaultMaxOutputTokens
	}
	target := ssestream.Target{
		Config: ssestream.Config{
			HTTPClient:     cfg.HTTPClient,
			ConnectTimeout: cfg.ConnectTimeout,
			IdleTimeout:    cfg.IdleTimeout,
			MaxLineBytes:   cfg.MaxLineBytes,
		},
		Dialect:    "responses",
		Headers:    cfg.Headers,
		APIKey:     cfg.APIKey,
		APIKeyFunc: cfg.APIKeyFunc,
	}
	if cfg.BaseURL != "" {
		target.URL = cfg.BaseURL + "/responses"
	}
	return &Provider{cfg: cfg, target: target}
}

// Name returns the provider id.
func (p *Provider) Name() string { return p.cfg.Name }

// Stream performs one streaming Responses request.
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
	instructions, input := buildInput(req.Messages)
	if input == nil {
		input = []inputItem{}
	}
	r := request{
		Model:           req.Model,
		Instructions:    instructions,
		Input:           input,
		Stream:          true,
		Tools:           provider.MapTools(req.Tools, flattenTool),
		ToolChoice:      req.ToolChoice,
		Temperature:     req.Temperature,
		MaxOutputTokens: cmp.Or(max(req.MaxTokens, 0), p.cfg.MaxOutputTokens),
	}
	if req.ReasoningEffort != "" {
		r.Reasoning = &reasoning{Effort: req.ReasoningEffort}
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("responses: encode request: %w", err)
	}
	return provider.MergeJSON(data, req.ProviderOptions)
}

// consume reads the event stream into a Response. A stream that ends before a
// terminal event is an error, returned with the text delivered so far.
func consume(s *ssestream.Stream, model string, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	c := provider.NewCollector(model, onEvent)
	finish := ""
	terminal := false

	err := s.Each(func(raw ssestream.Event) (bool, error) {
		var ev event
		if err := raw.Decode(&ev); err != nil {
			return false, err
		}
		ev.Type = cmp.Or(ev.Type, raw.Name)
		switch ev.Type {
		case "response.output_text.delta":
			c.Text(ev.Delta)
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			c.Reasoning(ev.Delta)
		case "response.output_item.added":
			if ev.Item.Type == "function_call" {
				c.ToolCall(ev.OutputIndex, ev.Item.CallID, "", ev.Item.Name, "")
			}
		case "response.function_call_arguments.delta":
			c.ToolCall(ev.OutputIndex, "", "", "", ev.Delta)
		case "response.completed":
			terminal, finish = true, "stop"
			c.Usage(usageOf(ev))
		case "response.incomplete":
			terminal = true
			finish = incompleteFinish(ev.Response.IncompleteDetails.Reason)
		case "response.failed":
			e := ev.Response.Error
			return false, failure(e.Code, cmp.Or(e.Message, ev.Error.Message), "response failed")
		case "error":
			return false, failure(cmp.Or(ev.Code, ev.Error.Code), cmp.Or(ev.Message, ev.Error.Message), "responses error")
		}
		return terminal, nil
	})
	switch {
	case err != nil:
		return c.Partial(), err
	case !terminal:
		return c.Partial(), &provider.Error{Message: "stream ended before response.completed", Retryable: true}
	}
	return c.Finish(finish), nil
}

// usageOf maps a completed response's usage.
func usageOf(ev event) provider.Usage {
	u := ev.Response.Usage
	return provider.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		ReasoningTokens:  u.OutputDetails.ReasoningTokens,
		CachedTokens:     u.InputDetails.CachedTokens,
		TotalTokens:      u.TotalTokens,
	}
}

// incompleteFinish maps the reason a response stopped early to a finish reason.
func incompleteFinish(reason string) string {
	switch reason {
	case "max_output_tokens":
		return "length"
	case "content_filter":
		return "content_filter"
	default:
		return "stop"
	}
}

// failure builds the non-retryable error for a failed response or an error
// event. fallback is the message when the server gave none.
func failure(code any, message, fallback string) *provider.Error {
	return &provider.Error{Code: ssestream.CodeString(code), Message: cmp.Or(message, fallback)}
}
