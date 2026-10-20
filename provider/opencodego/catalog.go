package opencodego

import (
	"slices"
	"sync"

	"github.com/AutumnsGrove/nova/provider"
)

// idPrefix starts every catalog id; the wire slug is the part after it.
const idPrefix = "opencode-go/"

// baselineModel is one entry of the code-defined catalog.
type baselineModel struct {
	id         string
	name       string
	dialect    Dialect
	context    int
	inPerM     float64
	outPerM    float64
	multimodal bool
}

// baseline is the code-defined catalog. It is the offline fallback and the
// source of the endpoint table; live discovery adds models on top of it. Prices
// are the Go table's lower tier (per million tokens), fetched 2026-10-13; a
// prompt-size-tiered model uses the lower tier, so cost is an estimate.
//
// Context windows are a conservative 200,000 tokens until discovery reads the
// live metadata, because the Go page does not list them.
var baseline = []baselineModel{
	{idPrefix + "deepseek-v4.1-flash", "DeepSeek V4.1 Flash", DialectChat, 200_000, 0.15, 0.60, false},
	{idPrefix + "glm-5.3", "GLM-5.3", DialectChat, 200_000, 1.40, 4.40, false},
	{idPrefix + "glm-5.3-flash", "GLM-5.3-Flash", DialectChat, 200_000, 0.15, 0.50, false},
	{idPrefix + "kimi-k3", "Kimi K3", DialectChat, 200_000, 3.00, 15.00, false},
	{idPrefix + "grok-4.7", "Grok 4.7", DialectResponses, 200_000, 2.00, 6.00, true},
	{idPrefix + "claude-haiku-5-5", "Claude Haiku 5.5", DialectMessages, 200_000, 0.10, 0.50, true},
	{idPrefix + "minimax-m3", "MiniMax M3", DialectMessages, 200_000, 0.30, 1.20, false},
}

// baselineFor returns the baseline entry for a catalog id.
func baselineFor(id string) (baselineModel, bool) {
	i := slices.IndexFunc(baseline, func(b baselineModel) bool { return b.id == id })
	if i < 0 {
		return baselineModel{}, false
	}
	return baseline[i], true
}

// state is the live catalog. Discovery refreshes it from a background goroutine
// while turns are already routing, so every access goes through mu.
var (
	mu         sync.RWMutex
	discovered []Discovered

	// Catalog is the baseline plus the live overlay, rebuilt by RegisterModels.
	// It is replaced whole, never edited in place; read it only when no refresh
	// can be running, and use APIFor to route.
	Catalog []provider.Model

	// EndpointTable records the dialect for every catalog id, baseline and
	// discovered. RegisterModels replaces it whole, so the same rule as Catalog
	// applies.
	EndpointTable = map[string]Dialect{}
)

// RegisterModels registers the baseline catalog, the cached live overlay, and
// the endpoint table. It is idempotent, so a reload can call it again.
func RegisterModels() {
	mu.RLock()
	overlay := slices.Clone(discovered)
	mu.RUnlock()

	catalog := make([]provider.Model, 0, len(baseline)+len(overlay))
	table := make(map[string]Dialect, len(baseline)+len(overlay))
	add := func(m provider.Model, d Dialect) {
		m = withBaselineModalities(m)
		provider.RegisterModel(m)
		catalog = append(catalog, m)
		table[m.ID] = d
	}
	for _, b := range baseline {
		add(provider.Model{
			ID:            b.id,
			Name:          b.name,
			Provider:      ProviderName,
			Slug:          stripPrefix(b.id),
			ContextWindow: b.context,
			InPerM:        b.inPerM,
			OutPerM:       b.outPerM,
		}, b.dialect)
	}
	for _, d := range overlay {
		add(d.model(), dialectFor(d.ID))
	}

	mu.Lock()
	Catalog, EndpointTable = catalog, table
	mu.Unlock()
}

// withBaselineModalities restores the image flag on a model the baseline lists
// as multimodal. Discovery does not report input modalities, so without this a
// live entry would replace the baseline one and lose the flag; anything else
// stays text only.
func withBaselineModalities(m provider.Model) provider.Model {
	if b, ok := baselineFor(m.ID); ok && b.multimodal && len(m.InputModalities) == 0 {
		m.InputModalities = []string{"text", "image"}
	}
	return m
}

// dialectFor returns the dialect for a catalog id: the explicit baseline table
// first, then inference from the model id.
func dialectFor(id string) Dialect {
	if b, ok := baselineFor(id); ok {
		return b.dialect
	}
	return inferDialect(stripPrefix(id))
}

func init() { RegisterModels() }
