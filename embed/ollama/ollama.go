// Package ollama implements embed.Embedder against a local Ollama instance:
// POST /api/embed, which takes a batch and returns one embedding per input.
//
// It embeds the older /api/embeddings endpoint's one-text shape into the same
// call, because a backfill on a local model is only practical as a batch. The
// vectors are L2-normalized by Ollama, and the call has no cost and no token
// count, which is a real answer for a local model rather than an unreported one.
package ollama

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

	"github.com/AutumnsGrove/nova/embed"
	"github.com/AutumnsGrove/nova/provider"
)

const (
	// DefaultBaseURL is where a local Ollama listens.
	DefaultBaseURL = "http://localhost:11434"
	// maxBody bounds a response so a broken or hostile server cannot exhaust
	// memory.
	maxBody = 32 << 20
)

// Config configures an Ollama embeddings client.
type Config struct {
	// BaseURL overrides the Ollama root. Default DefaultBaseURL.
	BaseURL string
	// HTTPClient overrides the default client.
	HTTPClient *http.Client
}

// Client is an Ollama embeddings backend.
type Client struct {
	baseURL string
	http    *http.Client
}

var _ embed.Embedder = (*Client)(nil)

// New builds an Ollama client.
func New(cfg Config) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		// Long, because a batch on a small CPU-only model can be minutes of
		// work rather than seconds. The caller's context bounds it.
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Client{baseURL: baseURL, http: client}
}

type requestWire struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type responseWire struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed sends one embedding call, chunking its inputs to embed.MaxBatch.
func (c *Client) Embed(ctx context.Context, req embed.Request) (*embed.Response, error) {
	if err := req.Validate(); err != nil {
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
		vectors, err := c.chunk(ctx, req.Model, input[start:end])
		if err != nil {
			return nil, err
		}
		dims := len(vectors[0])
		if out.Dims == 0 {
			out.Dims = dims
		} else if dims != out.Dims {
			return nil, fmt.Errorf("embed: response dimensions changed between chunks (%d then %d)", out.Dims, dims)
		}
		out.Vectors = append(out.Vectors, vectors...)
	}
	if err := embed.ValidateResponse(req, out); err != nil {
		return nil, err
	}
	return out, nil
}

// chunk sends one wire request. Ollama returns vectors in input order, so there
// is no index to remap; the count is still checked.
func (c *Client) chunk(ctx context.Context, model string, input []string) ([][]float32, error) {
	body, err := json.Marshal(requestWire{Model: model, Input: input})
	if err != nil {
		return nil, fmt.Errorf("embed: encoding request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("embed: request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("embed: reading response: %w", err)
	}
	if len(data) > maxBody {
		return nil, errors.New("embed: response body too large")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, data)
	}
	var wire responseWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("embed: parsing response: %w", err)
	}
	if len(wire.Embeddings) != len(input) {
		return nil, fmt.Errorf("embed: response has %d vectors for %d inputs", len(wire.Embeddings), len(input))
	}
	for i, vector := range wire.Embeddings {
		if len(vector) != len(wire.Embeddings[0]) {
			return nil, fmt.Errorf("embed: response vector %d has %d components, want %d", i, len(vector), len(wire.Embeddings[0]))
		}
	}
	return wire.Embeddings, nil
}

// statusError maps a non-200 response to the provider's typed error.
func statusError(resp *http.Response, body []byte) *provider.Error {
	retryable, after := provider.Classify(resp.StatusCode, resp.Header.Get("Retry-After"))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &provider.Error{Status: resp.StatusCode, Message: message, Retryable: retryable, RetryAfter: after}
}
