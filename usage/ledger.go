// Package usage keeps the running token and cost ledger for a session, with a
// catalog price fallback when a provider does not report cost itself.
package usage

import (
	"math"
	"sync"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
)

// cachedDiscount is the fraction of the input price a cached token costs.
const cachedDiscount = 0.1

// Ledger accumulates usage per model and in total. It is safe for concurrent
// use.
type Ledger struct {
	cat func(id string) (provider.Model, bool)

	mu      sync.Mutex
	total   provider.Usage
	byModel map[string]provider.Usage
}

// NewLedger builds an empty ledger. cat resolves a model id for cost fallback;
// it may be nil.
func NewLedger(cat func(id string) (provider.Model, bool)) *Ledger {
	return &Ledger{cat: cat, byModel: map[string]provider.Usage{}}
}

// Add fills in a usage record's cost, adds it to the total and to the model's
// entry, and returns the filled-in record.
func (l *Ledger) Add(model string, u provider.Usage) provider.Usage {
	var m provider.Model
	if l.cat != nil {
		m, _ = l.cat(model)
	}
	usd, source := CostFor(m, u)
	u.CostUSD = usd
	u.CostSource = source

	l.mu.Lock()
	l.total.Add(u)
	cur := l.byModel[model]
	cur.Add(u)
	l.byModel[model] = cur
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
	out := make(map[string]provider.Usage, len(l.byModel))
	for k, v := range l.byModel {
		out[k] = v
	}
	return out
}

// Rebuild replaces the ledger's contents from the message and compaction
// entries' usage, in file order. It is idempotent.
func (l *Ledger) Rebuild(entries []session.Entry) {
	l.mu.Lock()
	l.total = provider.Usage{}
	l.byModel = map[string]provider.Usage{}
	l.mu.Unlock()
	for _, e := range entries {
		if e.Usage == nil {
			continue
		}
		if e.Type != session.TypeMessage && e.Type != session.TypeCompaction {
			continue
		}
		l.Add(e.Model, *e.Usage)
	}
}

// CostFor returns the cost of one usage record: the provider's own number when
// it reported a positive cost, otherwise a catalog estimate. An unknown model
// or an unpriced one gives (0, "").
func CostFor(m provider.Model, u provider.Usage) (usd float64, source string) {
	if math.IsNaN(u.CostUSD) {
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
