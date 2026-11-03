// Package openrouter is the OpenRouter preset for embed.Embedder: one client
// that embeds through the OpenAI-compatible endpoint and reads the live
// embeddings roster.
//
// It keeps the shape decide/openrouter established: a default base URL, the key
// resolved per request, usage.cost passed through, and a Catalog over the same
// credential. A caller names a model or a role, never a vendor.
package openrouter

import (
	"context"
	"net/http"
	"strings"

	"github.com/AutumnsGrove/nova/embed"
	"github.com/AutumnsGrove/nova/embed/openai"
)

const (
	// ProviderName is the provider id recorded on a roster entry.
	ProviderName = "openrouter"
	// DefaultBaseURL is OpenRouter's API root.
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	// DefaultModel is the cheap hosted default: 1024 dimensions, 32K context,
	// and a fraction of a cent for a large backfill.
	DefaultModel = "perplexity/pplx-embed-v1-0.6b"
)

// Config configures an OpenRouter embeddings client.
type Config struct {
	// APIKey is sent as a bearer token. APIKeyFunc wins when both are set.
	APIKey string
	// APIKeyFunc supplies the key per request, so a credential resolved by
	// auth.Resolve is read fresh on every call.
	APIKeyFunc func(ctx context.Context) (string, error)
	// BaseURL overrides the API root, for a proxy. Default DefaultBaseURL.
	BaseURL string
	// HTTPClient overrides the default client.
	HTTPClient *http.Client
}

// Client is an OpenRouter embeddings backend and roster reader.
type Client struct {
	embedder *openai.Client
	key      func(context.Context) (string, error)
	baseURL  string
	http     *http.Client
}

var _ embed.Embedder = (*Client)(nil)
var _ embed.Catalog = (*Client)(nil)

// New builds the preset. It never fails; a missing key is reported by the first
// request.
func New(cfg Config) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	embedder := openai.New(openai.Config{
		APIKey:     cfg.APIKey,
		APIKeyFunc: cfg.APIKeyFunc,
		BaseURL:    baseURL,
		HTTPClient: client,
	})
	key := cfg.APIKeyFunc
	if key == nil {
		key = func(context.Context) (string, error) { return cfg.APIKey, nil }
	}
	return &Client{embedder: embedder, key: key, baseURL: baseURL, http: client}
}

// Embed delegates to the OpenAI-compatible adapter, which already chunks and
// maps the response by index.
func (c *Client) Embed(ctx context.Context, req embed.Request) (*embed.Response, error) {
	return c.embedder.Embed(ctx, req)
}
