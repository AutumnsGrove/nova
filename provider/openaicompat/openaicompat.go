// Package openaicompat implements the OpenAI chat-completions API with
// streaming and function/tool calling. It is the one wire-level provider the
// harness ships with; presets such as openrouter, ollama, and lmstudio are
// thin configurations on top of it.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AutumnsGrove/nova/provider"
)

// Defaults for the timeouts and the line cap. Zero in a Config means "use the
// default", so a caller cannot accidentally ask for no timeout at all.
const (
	defaultConnectTimeout = 30 * time.Second
	defaultIdleTimeout    = 120 * time.Second
	defaultMaxLineBytes   = 8 << 20
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
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = defaultIdleTimeout
	}
	if cfg.MaxLineBytes == 0 {
		cfg.MaxLineBytes = defaultMaxLineBytes
	}
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

	// reqCtx is the request context; the connect timer cancels it if the
	// headers never arrive. It must outlive Do so the body stays readable, so
	// cancel is deferred to the end of Stream rather than called here.
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build request: %w", err)
	}
	// Header precedence: fixed first, then the request's extras, then the
	// bearer token last so a stray Authorization cannot shadow the key.
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range p.cfg.Headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range req.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	var timedOut atomic.Bool
	timer := time.AfterFunc(p.cfg.ConnectTimeout, func() {
		timedOut.Store(true)
		cancel()
	})
	resp, err := p.client.Do(httpReq)
	timer.Stop()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if timedOut.Load() {
			return nil, &provider.Error{Message: "connect timeout", Retryable: true, Err: err}
		}
		return nil, &provider.Error{Message: "request failed", Retryable: true, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, httpError(resp.StatusCode, resp.Header, readCapped(resp.Body))
	}
	return p.consume(ctx, cancel, resp.Body, req, onEvent)
}

// buildBody renders the request, drops reasoning unless asked to keep it, and
// merges ProviderOptions into the top level so an option can add or override a
// field. The caller's Request is never modified.
func (p *Provider) buildBody(req provider.Request) ([]byte, error) {
	msgs := make([]wireMessage, len(req.Messages))
	for i, m := range req.Messages {
		wm := wireMessage{
			Role:       m.Role,
			Content:    m.Content,
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
