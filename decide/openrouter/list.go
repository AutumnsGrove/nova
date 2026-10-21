package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
)

// List reads OpenRouter's live decision-model roster
// (GET {base}/models?output_modalities=decisions), so a benchmark never
// hardcodes candidates while the lineup moves. The catalog is public, so an
// empty API key still works. A non-200 or unparseable body is an error, never an
// empty slice: an outage must not look like "no candidates".
func List(ctx context.Context, cfg Config) ([]provider.Model, error) {
	c := New(cfg)
	key, err := c.key(ctx)
	if err != nil {
		return nil, err
	}
	url := c.baseURL + "/models?output_modalities=decisions"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("decide: creating catalog request: %w", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.http.Do(req)
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
	return decodeModels(data)
}

// modelWire is the subset of OpenRouter's model entry the decision roster needs.
// Pricing is OpenRouter's decimal string of dollars per token.
type modelWire struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	Pricing       struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
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
		return nil, fmt.Errorf("decide: parsing model catalog: %w", err)
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
			OutPerM:         perMillion(m.Pricing.Completion),
			InputModalities: m.Architecture.InputModalities,
			// The query already filtered to decision models, so an entry whose
			// architecture omits the modality still supports decisions.
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

// outputModalities keeps the server's list but always includes decisions, since
// that is what the query asked for.
func outputModalities(in []string) []string {
	for _, m := range in {
		if m == "decisions" {
			return in
		}
	}
	out := make([]string, 0, len(in)+1)
	out = append(out, in...)
	return append(out, "decisions")
}
