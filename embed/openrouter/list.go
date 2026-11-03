package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
)

// maxBody bounds the roster response.
const maxBody = 8 << 20

// List reads OpenRouter's live embeddings roster. The unfiltered /models list
// omits embedding models entirely, so the output_modalities filter is what makes
// them visible. Dimensions are not reported here, only by a real response.
func (c *Client) List(ctx context.Context) ([]provider.Model, error) {
	key, err := c.key(ctx)
	if err != nil {
		return nil, fmt.Errorf("embed: resolving API key: %w", err)
	}
	url := c.baseURL + "/models?output_modalities=embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("embed: creating catalog request: %w", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.http.Do(req)
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
		return nil, fmt.Errorf("embed: response body too large")
	}
	if resp.StatusCode != http.StatusOK {
		retryable, after := provider.Classify(resp.StatusCode, resp.Header.Get("Retry-After"))
		return nil, &provider.Error{
			Status:     resp.StatusCode,
			Message:    truncate(strings.TrimSpace(string(data)), 500),
			Retryable:  retryable,
			RetryAfter: after,
		}
	}
	return decodeModels(data)
}

// List reads the roster with a one-shot config, for a caller that does not
// already hold a Client.
func List(ctx context.Context, cfg Config) ([]provider.Model, error) {
	return New(cfg).List(ctx)
}

// modelWire is the subset of OpenRouter's model entry the embeddings roster
// needs. Pricing is OpenRouter's decimal string of dollars per token.
type modelWire struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	Pricing       struct {
		Prompt string `json:"prompt"`
	} `json:"pricing"`
	Architecture struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
}

// decodeModels maps the roster to catalog entries. A malformed price becomes
// zero (unknown) rather than failing the roster; an entry with no id is
// unusable and is skipped.
func decodeModels(data []byte) ([]provider.Model, error) {
	var wire struct {
		Data []modelWire `json:"data"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("embed: parsing model catalog: %w", err)
	}
	models := make([]provider.Model, 0, len(wire.Data))
	for _, m := range wire.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, provider.Model{
			ID:              m.ID,
			Name:            m.Name,
			Provider:        ProviderName,
			Slug:            m.ID,
			ContextWindow:   m.ContextLength,
			InPerM:          perMillion(m.Pricing.Prompt),
			InputModalities: m.Architecture.InputModalities,
			// The query already filtered to embedding models, so an entry that
			// omits the modality still supports embeddings.
			OutputModalities: outputModalities(m.Architecture.OutputModalities),
		})
	}
	return models, nil
}

// perMillion converts a dollars-per-token decimal string to dollars per million
// tokens. An unparseable or negative value is unknown, not fatal.
func perMillion(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 0 {
		return 0
	}
	return v * 1_000_000
}

// outputModalities keeps the server's list but always includes embeddings, since
// that is what the query asked for.
func outputModalities(in []string) []string {
	for _, m := range in {
		if m == "embeddings" {
			return in
		}
	}
	out := make([]string, 0, len(in)+1)
	out = append(out, in...)
	return append(out, "embeddings")
}

// truncate cuts an untrusted body for an error message.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
