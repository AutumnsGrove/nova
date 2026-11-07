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
	// implicitCaching is true when the upstream caches a repeated prefix on its
	// own, so Nova must not send a cache marker. Claude needs one; the rest
	// cache automatically, and an unknown gateway is safer without a parameter
	// it might reject.
	implicitCaching bool
	// efforts, toolChoice, tiers, and multipliers come from the live
	// /v1/models metadata, captured 2026-11-05. monthlyLimitUSD is the
	// Go ($10) plan's monthly allowance from the Go docs page, same date;
	// Plus and Teams plans raise it.
	efforts         []string
	toolChoice      []string
	tiers           []provider.PriceTier
	multipliers     []provider.CostMultiplier
	monthlyLimitUSD float64
}

// baseline is the code-defined catalog. It is the offline fallback and the
// source of the endpoint table; live discovery adds models on top of it. Prices
// are the Go table's lower tier (per million tokens), fetched 2026-10-13; a
// prompt-size-tiered model uses the lower tier, so cost is an estimate.
//
// Context windows are a conservative 200,000 tokens until discovery reads the
// live metadata, because the Go page does not list them.
var baseline = []baselineModel{
	{
		id: idPrefix + "deepseek-v4.1-flash", name: "DeepSeek V4.1 Flash", dialect: DialectChat,
		context: 200_000, inPerM: 0.15, outPerM: 0.60, multimodal: true, implicitCaching: true,
		efforts: []string{"low", "high", "max"}, toolChoice: []string{"auto", "none", "required", "tool"},
		multipliers:     []provider.CostMultiplier{{Factor: 2, Window: "weekdays 01:00-04:00, 06:00-10:00 UTC"}},
		monthlyLimitUSD: 100,
	},
	{
		id: idPrefix + "glm-5.3", name: "GLM-5.3", dialect: DialectChat,
		context: 200_000, inPerM: 1.40, outPerM: 4.40, implicitCaching: true,
		efforts: []string{"low", "high", "max"}, toolChoice: []string{"auto"},
		monthlyLimitUSD: 15,
	},
	{
		id: idPrefix + "glm-5.3-flash", name: "GLM-5.3-Flash", dialect: DialectChat,
		context: 200_000, inPerM: 0.15, outPerM: 0.50, implicitCaching: true,
		efforts: []string{"low", "high", "max"}, toolChoice: []string{"auto"},
		monthlyLimitUSD: 60,
	},
	{
		id: idPrefix + "kimi-k3", name: "Kimi K3", dialect: DialectChat,
		context: 200_000, inPerM: 3.00, outPerM: 15.00, implicitCaching: true,
		efforts: []string{"max"}, toolChoice: []string{"auto", "none", "required"},
		monthlyLimitUSD: 15,
	},
	{
		id: idPrefix + "grok-4.7", name: "Grok 4.7", dialect: DialectResponses,
		context: 200_000, inPerM: 2.00, outPerM: 6.00, multimodal: true, implicitCaching: true,
		efforts: []string{"low", "medium", "high", "xhigh"}, toolChoice: []string{"auto", "none", "required", "tool"},
		tiers:           []provider.PriceTier{{AboveTokens: 200000, InPerM: 4, OutPerM: 12}},
		monthlyLimitUSD: 15,
	},
	{
		id: idPrefix + "claude-haiku-5-5", name: "Claude Haiku 5.5", dialect: DialectMessages,
		context: 200_000, inPerM: 0.10, outPerM: 0.50, multimodal: true,
		efforts:         []string{"low", "medium", "high", "xhigh", "max"},
		toolChoice:      []string{"auto", "none", "required", "tool"},
		tiers:           []provider.PriceTier{{AboveTokens: 100000, InPerM: 0.50, OutPerM: 2.50}},
		monthlyLimitUSD: 15,
	},
	{
		id: idPrefix + "minimax-m3", name: "MiniMax M3", dialect: DialectMessages,
		context: 200_000, inPerM: 0.30, outPerM: 1.20, implicitCaching: true,
		toolChoice:      []string{"auto"},
		tiers:           []provider.PriceTier{{AboveTokens: 512000, InPerM: 0.60, OutPerM: 2.40}},
		monthlyLimitUSD: 60,
	},
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
		m = withBaselineFlags(m)
		provider.RegisterModel(m)
		catalog = append(catalog, m)
		table[m.ID] = d
	}
	for _, b := range baseline {
		add(provider.Model{
			ID:               b.id,
			Name:             b.name,
			Provider:         ProviderName,
			Slug:             stripPrefix(b.id),
			ContextWindow:    b.context,
			InPerM:           b.inPerM,
			OutPerM:          b.outPerM,
			ImplicitCaching:  b.implicitCaching,
			ReasoningEfforts: b.efforts,
			ToolChoice:       b.toolChoice,
			PriceTiers:       b.tiers,
			CostMultipliers:  b.multipliers,
			MonthlyLimitUSD:  b.monthlyLimitUSD,
		}, b.dialect)
	}
	for _, d := range overlay {
		add(d.model(), dialectFor(d.ID))
	}

	mu.Lock()
	Catalog, EndpointTable = catalog, table
	mu.Unlock()
}

// withBaselineFlags restores what discovery does not report on a model the
// baseline lists: image input, and whether the upstream caches a prefix by
// itself. Without this a live entry would replace the baseline one and lose
// both; anything else defaults to text only and to needing an explicit cache
// marker.
func withBaselineFlags(m provider.Model) provider.Model {
	if b, ok := baselineFor(m.ID); ok {
		if b.multimodal && len(m.InputModalities) == 0 {
			m.InputModalities = []string{"text", "image"}
		}
		if b.implicitCaching {
			m.ImplicitCaching = true
		}
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
