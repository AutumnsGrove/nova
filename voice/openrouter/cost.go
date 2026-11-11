package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/AutumnsGrove/nova/voice"
)

// Speech is priced per character, not per token: OpenRouter bills media models
// "per character or minute of audio", and a speech model's pricing.prompt is
// dollars per character in the unit its provider bills. So a synthesis costs
// runes * pricePerMillion / 1e6, with no tokeniser involved.
//
// The roster reports the cheapest endpoint's price, which is not what a pinned
// request pays: Kokoro is $0.62/M characters on DeepInfra and $4.00/M on
// Together. A pin therefore needs the endpoint table, which is read per model and
// cached.
//
// prices caches both tables for one client, so costing a clip costs no request
// once they are warm.
type prices struct {
	// roster is the cheapest endpoint's price per model, nil until read.
	roster map[string]float64
	// endpoints maps a model to its providers' prices, nil until read.
	endpoints map[string]map[string]float64
}

// endpointWire is the subset of /models/{id}/endpoints a pin needs.
type endpointWire struct {
	Data struct {
		Endpoints []struct {
			ProviderName string `json:"provider_name"`
			Pricing      struct {
				Prompt string `json:"prompt"`
			} `json:"pricing"`
		} `json:"endpoints"`
	} `json:"data"`
}

// pricePerMillion returns the per-million-character price to estimate model at,
// given the request's provider pin. A pin that names exactly one endpoint is
// priced from that endpoint; anything else is priced at the roster's rate, which
// is the cheapest endpoint's. ok is false when the price cannot be known, because
// an unknown price is not zero dollars.
func (c *Client) pricePerMillion(ctx context.Context, model string, pin map[string]any) (float64, bool) {
	if !c.costLookup {
		return 0, false
	}
	c.costMu.Lock()
	defer c.costMu.Unlock()
	if name, pinned := pinnedProvider(pin); pinned {
		table, err := c.endpointPrices(ctx, model)
		if err != nil {
			// A pinned request that cannot be priced is left unknown rather than
			// billed at the cheapest rate: the pin exists to buy a costlier
			// endpoint, so the cheapest figure would understate it.
			return 0, false
		}
		price, ok := table[name]
		return price, ok && price > 0
	}
	price, ok := c.rosterPrices(ctx)[model]
	return price, ok && price > 0
}

// rosterPrices reads GET /models?output_modalities=speech once per client. A
// failure is not cached, so a transient one does not disable costing for good.
func (c *Client) rosterPrices(ctx context.Context) map[string]float64 {
	if c.prices.roster != nil {
		return c.prices.roster
	}
	models, err := c.List(ctx, voice.KindSpeech)
	if err != nil {
		return nil
	}
	table := make(map[string]float64, len(models))
	for _, m := range models {
		table[m.ID] = m.InPerM
	}
	c.prices.roster = table
	return table
}

// endpointPrices reads one model's endpoint table, cached per model. The provider
// names are the ones a config pin writes.
func (c *Client) endpointPrices(ctx context.Context, model string) (map[string]float64, error) {
	if table, ok := c.prices.endpoints[model]; ok {
		return table, nil
	}
	key, err := c.resolveKey(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := c.baseURL + "/models/" + escapeModelPath(model) + "/endpoints"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("voice: creating endpoint request: %w", err)
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
	var wire endpointWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("voice: parsing endpoint list: %w", err)
	}
	table := make(map[string]float64, len(wire.Data.Endpoints))
	for _, e := range wire.Data.Endpoints {
		if e.ProviderName != "" {
			table[e.ProviderName] = perMillion(e.Pricing.Prompt)
		}
	}
	if c.prices.endpoints == nil {
		c.prices.endpoints = make(map[string]map[string]float64, 1)
	}
	c.prices.endpoints[model] = table
	return table, nil
}

// pinnedProvider reports whether a pin names exactly one provider, which is the
// only case a price can be attributed. A pin with several names, or one that
// routes by order or ignore, leaves the choice to the API.
func pinnedProvider(pin map[string]any) (string, bool) {
	only, ok := pin["only"]
	if !ok {
		return "", false
	}
	var names []string
	switch v := only.(type) {
	case []string:
		names = v
	case []any:
		for _, item := range v {
			if name, ok := item.(string); ok {
				names = append(names, name)
			}
		}
	}
	if len(names) != 1 || names[0] == "" {
		return "", false
	}
	return names[0], true
}

// escapeModelPath escapes a catalog id for use as a path, keeping the slash that
// separates author from model.
func escapeModelPath(model string) string {
	author, slug, _ := strings.Cut(model, "/")
	return url.PathEscape(author) + "/" + url.PathEscape(slug)
}
