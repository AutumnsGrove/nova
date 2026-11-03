// Package openai implements embed.Embedder over an OpenAI-compatible
// embeddings endpoint: POST {base}/embeddings. It serves OpenAI, OpenRouter and
// every gateway that speaks the same shape.
//
// Batching lives here, not in the contract: one call chunks its inputs to
// embed.MaxBatch and reassembles them in input order. A failed chunk discards
// the whole call, because a caller that indexed the vectors it got would leave a
// silent hole.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/embed"
	"github.com/AutumnsGrove/nova/provider"
)

const (
	// DefaultBaseURL is OpenAI's API root.
	DefaultBaseURL = "https://api.openai.com/v1"
	// maxBody bounds a response so a broken or hostile server cannot exhaust
	// memory. A 128-input response at 3072 dimensions is a few megabytes.
	maxBody = 32 << 20
	// maxErrorBodyKept and maxErrorMessage bound what a failed response may put
	// in an error that reaches logs and the screen.
	maxErrorBodyKept = 8 << 10
	maxErrorMessage  = 500
)

// Config configures an embeddings client.
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

// Client is an OpenAI-compatible embeddings backend.
type Client struct {
	apiKey     string
	apiKeyFunc func(context.Context) (string, error)
	baseURL    string
	http       *http.Client
}

var _ embed.Embedder = (*Client)(nil)

// New builds an embeddings client. It never fails; a missing key is reported by
// the first request.
func New(cfg Config) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	return &Client{
		apiKey:     cfg.APIKey,
		apiKeyFunc: cfg.APIKeyFunc,
		baseURL:    baseURL,
		http:       client,
	}
}

// Embed sends one embedding call, chunking its inputs to embed.MaxBatch. It
// validates the request before the wire, so a malformed input is a local error
// rather than a 400 from the provider.
func (c *Client) Embed(ctx context.Context, req embed.Request) (*embed.Response, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	key, err := c.key(ctx)
	if err != nil {
		return nil, err
	}
	input := req.Input
	if req.Truncate {
		input = make([]string, len(req.Input))
		for i, text := range req.Input {
			input[i] = embed.TruncateText(text)
		}
	}

	out := &embed.Response{Model: req.Model}
	for start := 0; start < len(input); start += embed.MaxBatch {
		end := min(start+embed.MaxBatch, len(input))
		vectors, usage, reported, err := c.chunk(ctx, req.Model, input[start:end], key)
		if err != nil {
			// No partial vectors: a caller that indexed them would leave a hole.
			return nil, err
		}
		dims, err := chunkDims(vectors)
		if err != nil {
			return nil, err
		}
		if out.Dims == 0 {
			out.Dims = dims
		} else if dims != out.Dims {
			return nil, fmt.Errorf("embed: response dimensions changed between chunks (%d then %d)", out.Dims, dims)
		}
		out.Vectors = append(out.Vectors, vectors...)
		if reported {
			out.Usage.Add(usage)
		}
	}
	if err := embed.ValidateResponse(req, out); err != nil {
		return nil, err
	}
	return out, nil
}

// chunk sends one wire request and returns its vectors in input order.
func (c *Client) chunk(ctx context.Context, model string, input []string, key string) ([][]float32, provider.Usage, bool, error) {
	body, err := json.Marshal(requestWire{Model: model, Input: input, EncodingFormat: "float"})
	if err != nil {
		return nil, provider.Usage{}, false, fmt.Errorf("embed: encoding request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, provider.Usage{}, false, fmt.Errorf("embed: creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, provider.Usage{}, false, transportError(ctx, err)
	}
	defer resp.Body.Close()
	data, err := readBody(resp.Body)
	if err != nil {
		return nil, provider.Usage{}, false, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, provider.Usage{}, false, statusError(resp, data, key)
	}
	return decodeChunk(data, len(input))
}

// chunkDims checks that every vector in one chunk has the same length and
// returns it.
func chunkDims(vectors [][]float32) (int, error) {
	dims := 0
	for i, vector := range vectors {
		if len(vector) == 0 {
			return 0, fmt.Errorf("embed: response vector %d is empty", i)
		}
		if dims == 0 {
			dims = len(vector)
			continue
		}
		if len(vector) != dims {
			return 0, fmt.Errorf("embed: response vector %d has %d components, want %d", i, len(vector), dims)
		}
	}
	return dims, nil
}

// key resolves the API key for one request.
func (c *Client) key(ctx context.Context) (string, error) {
	if c.apiKeyFunc == nil {
		return c.apiKey, nil
	}
	key, err := c.apiKeyFunc(ctx)
	if err != nil {
		return "", fmt.Errorf("embed: resolving API key: %w", err)
	}
	return key, nil
}

// readBody reads a bounded response body.
func readBody(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("embed: reading response: %w", err)
	}
	if len(data) > maxBody {
		return nil, errors.New("embed: response body too large")
	}
	return data, nil
}

// errorBody is the provider's failure shape.
type errorBody struct {
	Error struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
	Message string `json:"message"`
}

// statusError maps a non-200 response to the provider's typed error. The API key
// is scrubbed from the message and the body, because a gateway may echo a
// rejected key back and neither reaches the screen or a log.
func statusError(resp *http.Response, body []byte, key string) *provider.Error {
	retryable, after := provider.Classify(resp.StatusCode, resp.Header.Get("Retry-After"))
	message := ""
	var e errorBody
	if json.Unmarshal(body, &e) == nil {
		message = e.Error.Message
		if message == "" {
			message = e.Message
		}
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &provider.Error{
		Status:     resp.StatusCode,
		Message:    truncateUTF8(scrub(message, key), maxErrorMessage),
		Body:       truncateUTF8(scrub(string(body), key), maxErrorBodyKept),
		Retryable:  retryable,
		RetryAfter: after,
	}
}

// scrub removes a credential from a string. A short key would match ordinary
// words, so it is left alone.
func scrub(s, secret string) string {
	if len(secret) < 8 {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// truncateUTF8 cuts s to at most n bytes without splitting a multi-byte rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// transportError keeps a cancellation a cancellation.
func transportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("embed: request failed: %w", err)
}
