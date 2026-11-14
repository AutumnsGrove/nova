// Package openai implements the voice contract over an OpenAI-compatible audio
// API: POST {base}/audio/speech and POST {base}/audio/transcriptions.
//
// One adapter covers three things the same shape already serves: OpenAI itself,
// any gateway, and the self-hosted engines — faster-whisper-server, whisper.cpp
// server, Kokoro-FastAPI and openedai-speech all answer these two endpoints. A
// local engine needs no key, so an empty key is not an error and sends no
// Authorization header.
//
// There is no roster and no cost estimate here. A local server answers with no
// usage block at all, so a synthesis is reported as zero cost with an empty
// CostSource: a free call is not an unknown one. A transcription that does
// report usage is priced from it.
package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/voice"
)

const (
	// DefaultBaseURL is OpenAI's API root. A local engine names its own, such
	// as http://127.0.0.1:8000/v1.
	DefaultBaseURL = "https://api.openai.com/v1"
	// DefaultTimeout bounds one audio request. A local engine on a small box
	// synthesizes slower than a hosted one, so this is as generous as the
	// OpenRouter preset's.
	DefaultTimeout = 120 * time.Second
)

// Config configures an OpenAI-compatible voice client.
type Config struct {
	// APIKey is sent as a bearer token. Empty sends no Authorization header,
	// which is what a local engine wants. APIKeyFunc wins when both are set.
	APIKey string
	// APIKeyFunc supplies the key per request, so a credential read from the
	// environment or a key store is not captured at construction.
	APIKeyFunc func(ctx context.Context) (string, error)
	// BaseURL overrides the API root. Default DefaultBaseURL.
	BaseURL string
	// HTTPClient overrides the default client.
	HTTPClient *http.Client
	// TranscribeJSON sends a base64 input_audio body instead of multipart, for
	// a server that only implements the JSON shape.
	TranscribeJSON bool
}

// Client is an OpenAI-compatible speech and transcription backend.
type Client struct {
	key            func(context.Context) (string, error)
	baseURL        string
	http           *http.Client
	transcribeJSON bool
}

var (
	_ voice.Speaker     = (*Client)(nil)
	_ voice.Transcriber = (*Client)(nil)
)

// New builds the adapter. It never fails; a bad base URL is reported by the
// first request, and a missing key is only an error for a server that wants one.
func New(cfg Config) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	key := cfg.APIKeyFunc
	if key == nil {
		key = func(context.Context) (string, error) { return cfg.APIKey, nil }
	}
	return &Client{key: key, baseURL: baseURL, http: client, transcribeJSON: cfg.TranscribeJSON}
}

// resolveKey reads the credential for one request. The empty string is a valid
// answer: a local engine takes no key.
func (c *Client) resolveKey(ctx context.Context) (string, error) {
	key, err := c.key(ctx)
	if err != nil {
		return "", fmt.Errorf("voice: resolving API key: %w", err)
	}
	return key, nil
}

// setHeaders adds the bearer token when there is one. An OpenAI-compatible
// endpoint takes no attribution header, so there is nothing else to add.
func (c *Client) setHeaders(req *http.Request, key string) {
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
}
