// Package openrouter implements decide.Decider over OpenRouter's decision
// surfaces. A Jev model id uses the older System One endpoint
// ({base}/systemone), which is the path Polaris runs in production; every other
// id uses the model-agnostic Decisions API ({host}/api/alpha/decisions).
// Callers choose a model, never a surface.
//
// The API key is sent only in the Authorization header and never appears in an
// error. A failed call is a *provider.Error, so a caller classifies a decision
// failure the same way it classifies a chat failure.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/provider"
)

const (
	// ProviderName is the provider id recorded on a decision model.
	ProviderName = "openrouter"
	// DefaultBaseURL is OpenRouter's API root. The Decisions API lives at the
	// host's /api/alpha/decisions, not under this root; see decisionsURL.
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	// maxBody bounds a response so a broken or hostile server cannot exhaust
	// memory. A full 22-question answer set is far smaller.
	maxBody = 8 << 20
)

// Config configures a decision client.
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

// Client is an OpenRouter decision backend.
type Client struct {
	apiKey     string
	apiKeyFunc func(context.Context) (string, error)
	baseURL    string
	http       *http.Client
}

var _ decide.Decider = (*Client)(nil)

// New builds a decision client. It never fails; a missing key is reported by
// the first request.
func New(cfg Config) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		apiKey:     cfg.APIKey,
		apiKeyFunc: cfg.APIKeyFunc,
		baseURL:    baseURL,
		http:       client,
	}
}

// Decide sends one decision request. It validates the request before the wire,
// so a malformed question is a local error rather than a 400 from the provider.
func (c *Client) Decide(ctx context.Context, req decide.Request) (*decide.Response, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	key, err := c.key(ctx)
	if err != nil {
		return nil, err
	}
	body, err := encodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("decide: encoding request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(req.Model), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decide: creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer resp.Body.Close()
	data, err := readBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, data)
	}
	return decodeResponse(req, data)
}

// key resolves the API key for one request.
func (c *Client) key(ctx context.Context) (string, error) {
	if c.apiKeyFunc == nil {
		return c.apiKey, nil
	}
	key, err := c.apiKeyFunc(ctx)
	if err != nil {
		return "", fmt.Errorf("decide: resolving API key: %w", err)
	}
	return key, nil
}

// endpoint picks the surface from the model id.
func (c *Client) endpoint(model string) string {
	if isJev(model) {
		return c.baseURL + "/systemone"
	}
	return decisionsURL(c.baseURL)
}

// decisionsURL derives {scheme}://{host}/api/alpha/decisions from the configured
// API root. Appending is the fallback for a root that does not parse as a URL,
// so the endpoint never comes back empty.
func decisionsURL(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(baseURL, "/") + "/api/alpha/decisions"
	}
	return u.Scheme + "://" + u.Host + "/api/alpha/decisions"
}

// isJev reports whether a model id names Jev, whose production path is the
// System One surface. The vendor prefix and an OpenRouter "~" alias marker are
// ignored; the final path segment is what names the model.
func isJev(model string) bool {
	id := strings.TrimPrefix(model, "~")
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		id = id[i+1:]
	}
	return strings.HasPrefix(strings.ToLower(id), "jev")
}

// readBody reads a bounded response body.
func readBody(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("decide: reading response: %w", err)
	}
	if len(data) > maxBody {
		return nil, errors.New("decide: response body too large")
	}
	return data, nil
}

// errorBody is the provider's failure shape.
type errorBody struct {
	Error struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// statusError maps a non-200 response to the provider's typed error. The API key
// is never part of it.
func statusError(resp *http.Response, body []byte) error {
	retryable, after := provider.Classify(resp.StatusCode, resp.Header.Get("Retry-After"))
	message := ""
	var e errorBody
	if json.Unmarshal(body, &e) == nil {
		message = e.Error.Message
	}
	if message == "" {
		message = truncate(strings.TrimSpace(string(body)), 200)
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &provider.Error{
		Status:     resp.StatusCode,
		Message:    message,
		Retryable:  retryable,
		RetryAfter: after,
	}
}

// transportError keeps a cancellation a cancellation and makes a deadline the
// same retryable error a role timeout produces.
func transportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return timeoutError(ctxErr)
		}
		return ctxErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return timeoutError(err)
	}
	return fmt.Errorf("decide: request failed: %w", err)
}

// timeoutError is the typed, retryable error a deadline produces.
func timeoutError(cause error) *provider.Error {
	return &provider.Error{
		Code:      "timeout",
		Message:   "decision request timed out",
		Retryable: true,
		Err:       cause,
	}
}

// truncate shortens an untrusted body for an error message without splitting a
// rune.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
