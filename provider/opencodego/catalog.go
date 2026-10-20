package opencodego

import "github.com/AutumnsGrove/nova/provider"

// baseline is the code-defined catalog. It is the offline fallback and the
// source of the endpoint table; live discovery adds models on top of it. Prices
// are the Go table's lower tier (per million tokens), fetched 2026-10-13; a
// prompt-size-tiered model uses the lower tier, so cost is an estimate.
//
// Context windows are a conservative 200,000 tokens until discovery reads the
// live metadata, because the Go page does not list them.
var baseline = []struct {
	id         string
	name       string
	dialect    Dialect
	context    int
	inPerM     float64
	outPerM    float64
	multimodal bool
	// implicitCaching is true when the upstream caches a repeated prefix on
	// its own, so Nova must not send a cache marker. Claude needs one; the
	// rest cache automatically, and an unknown gateway is safer without a
	// parameter it might reject.
	implicitCaching bool
}{
	{"opencode-go/deepseek-v4.1-flash", "DeepSeek V4.1 Flash", DialectChat, 200_000, 0.15, 0.60, false, true},
	{"opencode-go/glm-5.3", "GLM-5.3", DialectChat, 200_000, 1.40, 4.40, false, true},
	{"opencode-go/glm-5.3-flash", "GLM-5.3-Flash", DialectChat, 200_000, 0.15, 0.50, false, true},
	{"opencode-go/kimi-k3", "Kimi K3", DialectChat, 200_000, 3.00, 15.00, false, true},
	{"opencode-go/grok-4.7", "Grok 4.7", DialectResponses, 200_000, 2.00, 6.00, true, true},
	{"opencode-go/claude-haiku-5-5", "Claude Haiku 5.5", DialectMessages, 200_000, 0.10, 0.50, true, false},
	{"opencode-go/minimax-m3", "MiniMax M3", DialectMessages, 200_000, 0.30, 1.20, false, true},
}

// Catalog is the code-defined baseline, rebuilt by RegisterModels.
var Catalog []provider.Model

// EndpointTable records the dialect for every catalog id, baseline and
// discovered. It is rebuilt by RegisterModels.
var EndpointTable = map[string]Dialect{}

// RegisterModels registers the baseline catalog, the cached live overlay, and
// the endpoint table. It is idempotent, so a reload can call it again.
func RegisterModels() {
	Catalog = Catalog[:0]
	EndpointTable = map[string]Dialect{}
	for _, b := range baseline {
		addModel(provider.Model{
			ID:              b.id,
			Name:            b.name,
			Provider:        ProviderName,
			Slug:            stripPrefix(b.id),
			ContextWindow:   b.context,
			InPerM:          b.inPerM,
			OutPerM:         b.outPerM,
			ImplicitCaching: b.implicitCaching,
		}, b.dialect)
	}
	for _, m := range discoveredModels() {
		addModel(m, dialectFor(m.ID))
	}
}

// addModel registers one model and its endpoint. Discovery reports neither
// input modalities nor cache behaviour, so a model id the baseline lists keeps
// both flags when the live overlay replaces it; anything else defaults to text
// only and to needing an explicit cache marker.
func addModel(m provider.Model, d Dialect) {
	for _, b := range baseline {
		if b.id != m.ID {
			continue
		}
		if len(m.InputModalities) == 0 && b.multimodal {
			m.InputModalities = []string{"text", "image"}
		}
		if b.implicitCaching {
			m.ImplicitCaching = true
		}
		break
	}
	provider.RegisterModel(m)
	Catalog = append(Catalog, m)
	EndpointTable[m.ID] = d
}

// dialectFor returns the dialect for a catalog id: the explicit baseline table
// first, then inference from the model id.
func dialectFor(id string) Dialect {
	for _, b := range baseline {
		if b.id == id {
			return b.dialect
		}
	}
	return inferDialect(stripPrefix(id))
}

func init() { RegisterModels() }
