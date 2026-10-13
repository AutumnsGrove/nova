// Package opencodego is the OpenCode Go preset: one provider that sends each
// model to the wire dialect its endpoint uses (chat completions, Responses, or
// Messages), with a code-defined catalog and a live model overlay.
package opencodego

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/messages"
	"github.com/AutumnsGrove/nova/provider/openaicompat"
	"github.com/AutumnsGrove/nova/provider/responses"
)

const (
	// DefaultBaseURL is the OpenCode Go API root.
	DefaultBaseURL = "https://opencode.ai/zen/go/v1"
	// DefaultModel is the model a fresh config starts on.
	DefaultModel = "opencode-go/deepseek-v4.1-flash"
	// ProviderName is the provider id in the registry and the catalog.
	ProviderName = "opencodego"
	// SessionHeader carries the stable conversation id the docs ask for.
	SessionHeader = "x-opencode-session"
)

// Dialect is one wire API an OpenCode Go model speaks.
type Dialect string

const (
	DialectChat      Dialect = "chat_completions"
	DialectResponses Dialect = "responses"
	DialectMessages  Dialect = "messages"
)

// Config configures the preset.
type Config struct {
	APIKey         string
	APIKeyFunc     func(ctx context.Context) (string, error)
	BaseURL        string // default DefaultBaseURL
	HTTPClient     *http.Client
	ConnectTimeout time.Duration
	IdleTimeout    time.Duration
	// SessionID is sent as x-opencode-session when non-empty.
	SessionID string
	// UserAgent is sent on every request. Defaults to "nova".
	UserAgent string
}

// Provider routes each request to the dialect its model uses.
type Provider struct {
	chat      provider.Provider
	responses provider.Provider
	messages  provider.Provider
}

// New returns a provider.Provider named ProviderName that sends each request
// to the dialect its model uses.
func New(cfg Config) provider.Provider {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	userAgent := cfg.UserAgent
	if userAgent == "" {
		userAgent = "nova"
	}
	headers := map[string]string{"User-Agent": userAgent}
	if cfg.SessionID != "" {
		headers[SessionHeader] = cfg.SessionID
	}
	return &Provider{
		chat: openaicompat.New(openaicompat.Config{
			Name:           ProviderName,
			BaseURL:        base,
			APIKey:         cfg.APIKey,
			APIKeyFunc:     cfg.APIKeyFunc,
			Headers:        headers,
			HTTPClient:     cfg.HTTPClient,
			ConnectTimeout: cfg.ConnectTimeout,
			IdleTimeout:    cfg.IdleTimeout,
		}),
		responses: responses.New(responses.Config{
			Name:           ProviderName,
			BaseURL:        base,
			APIKey:         cfg.APIKey,
			APIKeyFunc:     cfg.APIKeyFunc,
			Headers:        headers,
			HTTPClient:     cfg.HTTPClient,
			ConnectTimeout: cfg.ConnectTimeout,
			IdleTimeout:    cfg.IdleTimeout,
		}),
		messages: messages.New(messages.Config{
			Name:           ProviderName,
			BaseURL:        base,
			APIKey:         cfg.APIKey,
			APIKeyFunc:     cfg.APIKeyFunc,
			Headers:        headers,
			HTTPClient:     cfg.HTTPClient,
			ConnectTimeout: cfg.ConnectTimeout,
			IdleTimeout:    cfg.IdleTimeout,
		}),
	}
}

// Name returns the provider id.
func (p *Provider) Name() string { return ProviderName }

// Stream dispatches to the dialect for req.Model.
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	switch APIFor(req.Model) {
	case DialectResponses:
		return p.responses.Stream(ctx, req, onEvent)
	case DialectMessages:
		return p.messages.Stream(ctx, req, onEvent)
	default:
		return p.chat.Stream(ctx, req, onEvent)
	}
}

// APIFor reports the dialect for a model id. It consults the catalog's endpoint
// table first, then infers from the model id, and defaults to chat completions.
func APIFor(model string) Dialect {
	if d, ok := EndpointTable[model]; ok {
		return d
	}
	return inferDialect(stripPrefix(model))
}

// stripPrefix removes the "opencode-go/" catalog prefix.
func stripPrefix(model string) string {
	if rest, ok := strings.CutPrefix(model, "opencode-go/"); ok {
		return rest
	}
	return model
}

// inferDialect mirrors OpenCode's endpoint table for a live model id that the
// code-defined catalog does not cover. The conservative default is chat.
func inferDialect(id string) Dialect {
	lower := strings.ToLower(id)
	switch {
	case strings.HasPrefix(lower, "claude-"), strings.HasPrefix(lower, "minimax-"):
		return DialectMessages
	case lower == "qwen3.8-max":
		return DialectChat
	case strings.HasPrefix(lower, "qwen"):
		return DialectMessages
	case strings.HasPrefix(lower, "gpt-"), strings.HasPrefix(lower, "grok"), strings.HasPrefix(lower, "muse-spark-"):
		return DialectResponses
	default:
		return DialectChat
	}
}
