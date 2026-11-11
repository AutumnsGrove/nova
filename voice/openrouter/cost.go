package openrouter

import (
	"context"

	"github.com/AutumnsGrove/nova/voice"
)

// catalogRunesPerToken estimates a tokeniser's ratio for prose. OpenRouter
// prices speech per input *token* (Kokoro is $0.62 per million on DeepInfra and
// $4.00 on Together), and Nova has no tokeniser, so a synthesis's cost is an
// estimate and never an invoice. Four characters to a token is the usual figure
// for English; CostSource reports the result as "catalog" so a reader knows it
// is derived rather than billed.
const catalogRunesPerToken = 4

// prices caches the roster's per-million-token price by model id. The roster is
// one request per client rather than one per clip, which is what the old
// /generation lookup cost for a number it never returned.
type prices struct {
	// table is nil until the roster has been read once.
	table map[string]float64
}

// pricePerMillion returns the catalog price for model, reading the speech
// roster on first use. ok is false when the model is absent, its price is
// unknown (zero), or the roster could not be read: an unknown price is not zero
// dollars, and the caller must leave the cost unset rather than report one.
//
// The lock is held across the fetch on purpose, so concurrent clips read the
// roster once between them instead of racing a request each. It covers only the
// cost step, which runs after a clip has already been synthesized.
func (c *Client) pricePerMillion(ctx context.Context, model string) (float64, bool) {
	if !c.costLookup {
		return 0, false
	}
	c.costMu.Lock()
	defer c.costMu.Unlock()
	if c.prices.table == nil {
		models, err := c.List(ctx, voice.KindSpeech)
		if err != nil {
			// Not cached: a transient failure must not disable costing for the
			// life of the client.
			return 0, false
		}
		table := make(map[string]float64, len(models))
		for _, m := range models {
			table[m.ID] = m.InPerM
		}
		c.prices.table = table
	}
	perMillion, ok := c.prices.table[model]
	return perMillion, ok && perMillion > 0
}
