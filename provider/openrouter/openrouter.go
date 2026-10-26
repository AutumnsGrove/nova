// Package openrouter provides an OpenRouter preset over the OpenAI-compatible
// provider. OpenRouter speaks chat completions, so the preset only supplies the
// base URL, the attribution headers OpenRouter asks for, and the API key.
// Routing preferences travel through Request.ProviderOptions as rendered by
// Routing.Options, and the model catalog lives in Catalog.
package openrouter

import (
	"context"
	"net/http"

	"github.com/AutumnsGrove/nova/provider/openaicompat"
)

// Defaults for the API root and the attribution headers.
const (
	// ProviderName is the provider id in the registry and the catalog.
	ProviderName = "openrouter"
	// DefaultBaseURL is OpenRouter's chat-completions API root.
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	// DefaultReferer is sent as HTTP-Referer for attribution.
	DefaultReferer = "https://github.com/AutumnsGrove/nova"
	// DefaultTitle is sent as X-Title for attribution.
	DefaultTitle = "Nova"
)

// Config configures an OpenRouter provider.
type Config struct {
	// APIKey is sent as a bearer token. APIKeyFunc wins when both are set.
	APIKey string
	// APIKeyFunc supplies the key per request, so a credential resolved by
	// auth.Resolve is read fresh on every call.
	APIKeyFunc func(ctx context.Context) (string, error)
	// BaseURL overrides the API root (useful for a proxy). Default DefaultBaseURL.
	BaseURL string
	// Referer is the HTTP-Referer attribution header. Default DefaultReferer.
	Referer string
	// Title is the X-Title attribution header. Default DefaultTitle.
	Title string
	// HTTPClient overrides the default client.
	HTTPClient *http.Client
	// EmptyToolCallContent sends `"content": ""` on a tool-call-only assistant
	// message instead of omitting it; see openaicompat.Config.
	EmptyToolCallContent bool
}

// New builds an OpenRouter provider with the attribution headers and the key
// wired in. It never fails; a missing key is reported by the first request.
func New(cfg Config) *openaicompat.Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Referer == "" {
		cfg.Referer = DefaultReferer
	}
	if cfg.Title == "" {
		cfg.Title = DefaultTitle
	}
	return openaicompat.New(openaicompat.Config{
		Name:       ProviderName,
		BaseURL:    cfg.BaseURL,
		APIKey:     cfg.APIKey,
		APIKeyFunc: cfg.APIKeyFunc,
		Headers: map[string]string{
			"HTTP-Referer": cfg.Referer,
			"X-Title":      cfg.Title,
		},
		HTTPClient:           cfg.HTTPClient,
		EmptyToolCallContent: cfg.EmptyToolCallContent,
	})
}
