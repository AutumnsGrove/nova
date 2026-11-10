package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// generationWire is the subset of /generation the cost lookup needs. total_cost
// is the call's real spend; usage is the same number under its older name, so it
// is a fallback rather than a second charge.
type generationWire struct {
	Data struct {
		APIType   string   `json:"api_type"`
		TotalCost *float64 `json:"total_cost"`
		Usage     *float64 `json:"usage"`
	} `json:"data"`
}

// generationCost resolves a synthesis's exact cost. The speech response carries
// no usage block, so this second request is the only exact price. It is best
// effort: a missing key, a slow lookup, a non-200 or a malformed body all leave
// the cost unknown, because a failed lookup is not a failed synthesis.
func (c *Client) generationCost(ctx context.Context, generationID string) (float64, bool) {
	ctx, cancel := context.WithTimeout(ctx, c.costTimeout)
	defer cancel()
	key, err := c.resolveKey(ctx)
	if err != nil {
		return 0, false
	}
	endpoint := c.baseURL + "/generation?id=" + url.QueryEscape(generationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, false
	}
	c.setHeaders(req, key)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	data, err := readBody(resp.Body, maxJSONBody)
	if err != nil {
		return 0, false
	}
	var wire generationWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return 0, false
	}
	switch {
	case wire.Data.TotalCost != nil && *wire.Data.TotalCost >= 0:
		return *wire.Data.TotalCost, true
	case wire.Data.Usage != nil && *wire.Data.Usage >= 0:
		return *wire.Data.Usage, true
	}
	return 0, false
}
