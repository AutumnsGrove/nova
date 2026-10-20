// Package openaicompat implements the OpenAI chat-completions API with
// streaming and function/tool calling. It is the one wire-level provider the
// harness ships with; presets such as openrouter, ollama, and lmstudio are
// thin configurations on top of it.
package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/ssestream"
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
	// APIKeyFunc, when set, supplies the key per request and wins over APIKey.
	// It lets a provider rotate or refresh credentials without rebuilding.
	APIKeyFunc func(ctx context.Context) (string, error)
	// Headers are added to every request (for example OpenRouter's
	// HTTP-Referer and X-Title).
	Headers map[string]string
	// HTTPClient overrides the default client.
	HTTPClient *http.Client
	// ConnectTimeout bounds the time to first response header. Default 30s.
	ConnectTimeout time.Duration
	// IdleTimeout bounds the gap between stream chunks. Default 120s.
	IdleTimeout time.Duration
	// MaxLineBytes bounds one SSE line. Default 8 MiB.
	MaxLineBytes int
	// SendReasoning re-sends Message.Reasoning to the provider. Default false,
	// because most providers reject a reasoning field they did not send.
	SendReasoning bool
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

// BaseURL returns the configured API root, trailing slash removed.
func (p *Provider) BaseURL() string { return p.cfg.BaseURL }

// Stream performs one streaming chat-completions request, accumulating text,
// reasoning, and tool-call argument deltas into a final Response. A failure
// after the stream has started still returns the partial Response alongside
// the error, so the caller can keep what the model already said.
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Model == "" {
		return nil, fmt.Errorf("openaicompat: model is required")
	}
	if p.cfg.BaseURL == "" {
		return nil, fmt.Errorf("openaicompat: base URL is required")
	}

	body, err := p.buildBody(req)
	if err != nil {
		return nil, err
	}

	key := p.cfg.APIKey
	if p.cfg.APIKeyFunc != nil {
		key, err = p.cfg.APIKeyFunc(ctx)
		if err != nil {
			return nil, &provider.Error{Message: "resolving API key", Retryable: false, Err: err}
		}
	}

	// Header precedence: fixed first, then the config's, then the request's
	// extras, then the bearer token last so a stray Authorization cannot shadow
	// the key.
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "text/event-stream")
	for k, v := range p.cfg.Headers {
		headers.Set(k, v)
	}
	for k, v := range req.ExtraHeaders {
		headers.Set(k, v)
	}
	if key != "" {
		headers.Set("Authorization", "Bearer "+key)
	}

	stream, err := ssestream.Open(ctx, ssestream.Config{
		HTTPClient:     p.client,
		ConnectTimeout: p.cfg.ConnectTimeout,
		IdleTimeout:    p.cfg.IdleTimeout,
		MaxLineBytes:   p.cfg.MaxLineBytes,
	}, p.cfg.BaseURL+"/chat/completions", headers, body)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	return p.consume(ctx, stream, req, onEvent)
}

// buildBody renders the request, drops reasoning unless asked to keep it, and
// merges ProviderOptions into the top level so an option can add or override a
// field. The caller's Request is never modified.
func (p *Provider) buildBody(req provider.Request) ([]byte, error) {
	msgs := make([]wireMessage, len(req.Messages))
	for i, m := range req.Messages {
		wm := wireMessage{
			Role:       m.Role,
			Content:    wireContent(m),
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		if p.cfg.SendReasoning {
			wm.Reasoning = m.Reasoning
		}
		msgs[i] = wm
	}
	cr := chatRequest{
		Model:           req.Model,
		Messages:        msgs,
		Tools:           req.Tools,
		ToolChoice:      req.ToolChoice,
		Temperature:     req.Temperature,
		Stop:            req.Stop,
		ReasoningEffort: req.ReasoningEffort,
		Stream:          true,
		StreamOptions:   map[string]bool{"include_usage": true},
		CacheControl:    provider.RequestCacheControl(req),
	}
	if req.MaxTokens > 0 {
		maxTokens := req.MaxTokens
		cr.MaxTokens = &maxTokens
	}
	raw, err := json.Marshal(cr)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: encode request: %w", err)
	}
	if len(req.ProviderOptions) == 0 {
		return raw, nil
	}
	var base map[string]any
	if err := json.Unmarshal(raw, &base); err != nil {
		return nil, fmt.Errorf("openaicompat: decode request for merge: %w", err)
	}
	merged, err := json.Marshal(provider.MergeOptions(base, req.ProviderOptions))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: encode merged request: %w", err)
	}
	return merged, nil
}
