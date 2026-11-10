package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/voice"
)

// modelWire is the subset of an OpenRouter model entry a voice roster needs.
// Pricing is OpenRouter's decimal string of dollars per token.
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
	SupportedVoices []string `json:"supported_voices"`
}

// List reads the live roster for one direction. The unfiltered /models list
// omits speech and transcription models, so the output_modalities filter is what
// makes them visible.
func (c *Client) List(ctx context.Context, kind voice.Kind) ([]provider.Model, error) {
	if !kind.Valid() {
		return nil, fmt.Errorf("voice: unknown roster kind %q", kind)
	}
	key, err := c.resolveKey(ctx)
	if err != nil {
		return nil, err
	}
	query := url.Values{"output_modalities": []string{string(kind)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("voice: creating catalog request: %w", err)
	}
	c.setHeaders(req, key)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer resp.Body.Close()
	data, err := readBody(resp.Body, maxJSONBody)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, data, key)
	}
	return decodeModels(data, kind)
}

// decodeModels maps the roster to catalog entries. A malformed price becomes
// zero (unknown) rather than failing the roster; an entry with no id is
// unusable and is skipped.
func decodeModels(data []byte, kind voice.Kind) ([]provider.Model, error) {
	var wire struct {
		Data []modelWire `json:"data"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("voice: parsing model catalog: %w", err)
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
			// The query already filtered to this direction, so an entry that
			// omits the modality still supports it.
			OutputModalities: withModality(m.Architecture.OutputModalities, string(kind)),
			Voices:           m.SupportedVoices,
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

// withModality keeps the server's list but always includes the direction the
// query asked for.
func withModality(in []string, want string) []string {
	for _, m := range in {
		if m == want {
			return in
		}
	}
	out := make([]string, 0, len(in)+1)
	out = append(out, in...)
	return append(out, want)
}
