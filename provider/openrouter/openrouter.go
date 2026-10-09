// Package openrouter provides an OpenRouter preset over the OpenAI-compatible
// provider. OpenRouter speaks chat-completions, so the only OpenRouter-specific
// parts are the base URL, the optional attribution headers, and the API key.
package openrouter

import (
	"net/http"
	"os"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/openaicompat"
)

// DefaultBaseURL is OpenRouter's API root.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// Option customizes the underlying OpenAI-compatible config.
type Option func(*openaicompat.Config)

// WithBaseURL overrides the API root (useful for a proxy).
func WithBaseURL(url string) Option {
	return func(c *openaicompat.Config) { c.BaseURL = url }
}

// WithHTTPReferer sets the HTTP-Referer attribution header.
func WithHTTPReferer(referer string) Option {
	return func(c *openaicompat.Config) { c.Headers["HTTP-Referer"] = referer }
}

// WithTitle sets the X-Title attribution header.
func WithTitle(title string) Option {
	return func(c *openaicompat.Config) { c.Headers["X-Title"] = title }
}

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(c *openaicompat.Config) { c.HTTPClient = client }
}

// New builds an OpenRouter provider. When apiKey is empty, OPENROUTER_API_KEY
// is read from the environment.
func New(apiKey string, opts ...Option) *openaicompat.Provider {
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	cfg := openaicompat.Config{
		Name:    "openrouter",
		BaseURL: DefaultBaseURL,
		APIKey:  apiKey,
		Headers: map[string]string{},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return openaicompat.New(cfg)
}

// Register builds an OpenRouter provider and adds it to the global provider
// registry, returning it for direct use.
func Register(apiKey string, opts ...Option) *openaicompat.Provider {
	p := New(apiKey, opts...)
	provider.Register(p)
	return p
}
