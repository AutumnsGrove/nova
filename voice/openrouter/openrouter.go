// Package openrouter is the OpenRouter preset for the voice contract: one client
// that synthesizes speech, transcribes audio, and reads the live roster.
//
// It keeps the shape embed/openrouter established: a default base URL, the
// attribution headers OpenRouter asks for, the key resolved per request, and a
// Catalog over the same credential. A caller names a model or a role, never a
// vendor.
package openrouter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/voice"
)

const (
	// ProviderName is the provider id recorded on a roster entry.
	ProviderName = "openrouter"
	// DefaultBaseURL is OpenRouter's API root.
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	// DefaultReferer is sent as HTTP-Referer for attribution.
	DefaultReferer = "https://github.com/AutumnsGrove/nova"
	// DefaultTitle is sent as X-Title for attribution.
	DefaultTitle = "Nova"
	// DefaultCostTimeout bounds the generation lookup, which must never delay a
	// synthesis for long.
	DefaultCostTimeout = 5 * time.Second
	// DefaultTimeout bounds one audio request. Transcription is capped by the
	// provider's own 60 s upstream timeout; synthesis of a long chunk can take
	// longer than a chat call, so this is generous.
	DefaultTimeout = 120 * time.Second
)

// Config configures an OpenRouter voice client.
type Config struct {
	// APIKey is sent as a bearer token. APIKeyFunc wins when both are set.
	APIKey string
	// APIKeyFunc supplies the key per request, so a credential resolved by
	// auth.Resolve is read fresh on every call.
	APIKeyFunc func(ctx context.Context) (string, error)
	// BaseURL overrides the API root, for a proxy. Default DefaultBaseURL.
	BaseURL string
	// Referer is the HTTP-Referer attribution header. Default DefaultReferer.
	Referer string
	// Title is the X-Title attribution header. Default DefaultTitle.
	Title string
	// HTTPClient overrides the default client.
	HTTPClient *http.Client
	// CostLookup asks the provider for the exact cost of each synthesis. It
	// costs one extra request per call, so a caller that never reads Usage
	// should leave it false.
	CostLookup bool
	// CostTimeout bounds one lookup. Default DefaultCostTimeout.
	CostTimeout time.Duration
}

// Client is an OpenRouter voice backend and roster reader.
type Client struct {
	key         func(context.Context) (string, error)
	baseURL     string
	referer     string
	title       string
	http        *http.Client
	costLookup  bool
	costTimeout time.Duration
}

var (
	_ voice.Speaker     = (*Client)(nil)
	_ voice.Transcriber = (*Client)(nil)
	_ voice.Catalog     = (*Client)(nil)
)

// New builds the preset. It never fails; a missing key is reported by the first
// request.
func New(cfg Config) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	referer := cfg.Referer
	if referer == "" {
		referer = DefaultReferer
	}
	title := cfg.Title
	if title == "" {
		title = DefaultTitle
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	costTimeout := cfg.CostTimeout
	if costTimeout <= 0 {
		costTimeout = DefaultCostTimeout
	}
	key := cfg.APIKeyFunc
	if key == nil {
		key = func(context.Context) (string, error) { return cfg.APIKey, nil }
	}
	return &Client{
		key:         key,
		baseURL:     baseURL,
		referer:     referer,
		title:       title,
		http:        client,
		costLookup:  cfg.CostLookup,
		costTimeout: costTimeout,
	}
}

// resolveKey reads the credential for one request.
func (c *Client) resolveKey(ctx context.Context) (string, error) {
	key, err := c.key(ctx)
	if err != nil {
		return "", fmt.Errorf("voice: resolving API key: %w", err)
	}
	return key, nil
}

// setHeaders adds the attribution headers every OpenRouter request carries, and
// the bearer token when there is one.
func (c *Client) setHeaders(req *http.Request, key string) {
	req.Header.Set("HTTP-Referer", c.referer)
	req.Header.Set("X-Title", c.title)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
}
