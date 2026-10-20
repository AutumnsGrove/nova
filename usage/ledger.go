// Package usage keeps the running token and cost ledger for a session, with a
// catalog price fallback when a provider does not report cost itself.
package usage

import (
	"maps"
	"math"
	"sync"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
)

// cachedDiscount is the fraction of the input price a cached token costs.
const cachedDiscount = 0.1

// tally is the running sums a Ledger guards with its lock.
type tally struct {
	total   provider.Usage
	byModel map[string]provider.Usage
}

func newTally() tally { return tally{byModel: map[string]provider.Usage{}} }

func (t *tally) add(model string, u provider.Usage) {
	t.total.Add(u)
	cur := t.byModel[model]
	cur.Add(u)
	t.byModel[model] = cur
}

// Ledger accumulates usage per model and in total. It is safe for concurrent
// use.
type Ledger struct {
	cat func(id string) (provider.Model, bool)

	mu sync.Mutex
	tally
}

// NewLedger builds an empty ledger. cat resolves a model id for cost fallback;
// it may be nil.
func NewLedger(cat func(id string) (provider.Model, bool)) *Ledger {
	return &Ledger{cat: cat, tally: newTally()}
}

// price fills in a usage record's cost and its source.
func (l *Ledger) price(model string, u provider.Usage) provider.Usage {
	var m provider.Model
	if l.cat != nil {
		m, _ = l.cat(model)
	}
	u.CostUSD, u.CostSource = CostFor(m, u)
	return u
}

// Add fills in a usage record's cost, adds it to the total and to the model's
// entry, and returns the filled-in record.
func (l *Ledger) Add(model string, u provider.Usage) provider.Usage {
	u = l.price(model, u)
	l.mu.Lock()
	l.add(model, u)
	l.mu.Unlock()
	return u
}

// Total returns the accumulated usage across every model.
func (l *Ledger) Total() provider.Usage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.total
}

// ByModel returns a copy of the per-model breakdown.
func (l *Ledger) ByModel() map[string]provider.Usage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.byModel)
}

// Rebuild replaces the ledger's contents from the message and compaction
// entries' usage, in file order. It is idempotent, and it swaps the new sums in
// at once so a concurrent reader never sees a half-built ledger.
func (l *Ledger) Rebuild(entries []session.Entry) {
	fresh := newTally()
	for _, e := range entries {
		if e.Usage == nil || (e.Type != session.TypeMessage && e.Type != session.TypeCompaction) {
			continue
		}
		fresh.add(e.Model, l.price(e.Model, *e.Usage))
	}
	l.mu.Lock()
	l.tally = fresh
	l.mu.Unlock()
}

// CostFor returns the cost of one usage record: the provider's own number when
// it reported a positive cost, otherwise a catalog estimate. A negative figure
// counts as not reported; a NaN or infinite one is garbage and costs nothing
// (D86). An unknown model or an unpriced one gives (0, "").
func CostFor(m provider.Model, u provider.Usage) (usd float64, source string) {
	if math.IsNaN(u.CostUSD) || math.IsInf(u.CostUSD, 1) {
		return 0, ""
	}
	if u.CostUSD > 0 {
		return u.CostUSD, "provider"
	}
	if m.ID == "" || (m.InPerM == 0 && m.OutPerM == 0) {
		return 0, ""
	}
	prompt := u.PromptTokens
	if prompt < 0 {
		prompt = 0
	}
	completion := u.CompletionTokens
	if completion < 0 {
		completion = 0
	}
	cached := u.CachedTokens
	if cached < 0 {
		cached = 0
	}
	if cached > prompt {
		cached = prompt
	}
	cost := float64(prompt-cached)*m.InPerM/1e6 +
		float64(cached)*m.InPerM/1e6*cachedDiscount +
		float64(completion)*m.OutPerM/1e6
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		return 0, ""
	}
	return cost, "catalog"
}
