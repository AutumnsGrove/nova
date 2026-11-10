package provider

import (
	"cmp"
	"maps"
	"slices"
	"sync"
)

// Model describes one selectable model. The catalog is code-defined, so a
// stored model id keeps resolving even when a provider changes its slug, and
// pricing is available for cost accounting and caps.
type Model struct {
	// ID is the stable id used in config and sessions.
	ID string
	// Name is the display name.
	Name string
	// Provider is the provider name this model is served by.
	Provider string
	// Slug is the provider's wire model id.
	Slug string
	// ContextWindow is the total token budget, when known.
	ContextWindow int
	// MaxOutputTokens bounds one response, when known.
	MaxOutputTokens int
	// InPerM and OutPerM are listed dollars per million tokens.
	InPerM  float64
	OutPerM float64
	// InputModalities lists the input kinds the model accepts, such as "text"
	// and "image". Empty means text only, the safe default for a model Nova
	// does not know. It mirrors OpenRouter's input_modalities.
	InputModalities []string
	// OutputModalities lists what the model produces, such as "text" or
	// "decisions". Empty means text only, the safe default for a chat model.
	// It mirrors OpenRouter's output_modalities.
	OutputModalities []string
	// Voices lists the voice identifiers a speech model accepts, such as
	// "af_heart" or "alloy". Empty means the roster did not report any, which
	// is the case for every model that is not a speech model. It mirrors
	// OpenRouter's supported_voices.
	Voices []string
	// ImplicitCaching reports that the provider caches a repeated request
	// prefix on its own, so Nova must not send an explicit cache marker.
	// False is the safe default: an anthropic-style model needs the marker to
	// cache at all, and a model the catalog does not know is never marked.
	ImplicitCaching bool
	// ReasoningEfforts lists the reasoning effort tiers the model accepts,
	// such as low, high, or max. Empty means the model has no effort
	// control; a model the catalog does not know sends none either.
	ReasoningEfforts []string
	// ToolChoice lists the tool_choice variants the model accepts, such as
	// auto, none, required, or tool. Empty means unknown: a model the
	// catalog does not describe is sent tools without a choice variant.
	ToolChoice []string
	// PriceTiers lists the prompt-size price bands above the base InPerM
	// and OutPerM rates, ascending by AboveTokens. Empty means flat
	// pricing at any prompt size.
	PriceTiers []PriceTier
	// CostMultipliers lists the time-based price factors, such as a peak
	// doubling. Display-only: scheduling around them is the caller's job.
	CostMultipliers []CostMultiplier
	// MonthlyLimitUSD is the subscription's monthly allowance for this
	// model in dollars, Go base plan. Plus and Teams plans raise it; a
	// model with no plan limit is zero. Docs-only data, not a price.
	MonthlyLimitUSD float64
}

// PriceTier is one prompt-size price band: above AboveTokens prompt tokens,
// InPerM and OutPerM replace the model's base rates.
type PriceTier struct {
	AboveTokens int
	InPerM      float64
	OutPerM     float64
}

// CostMultiplier is one time-based price factor with the window it applies
// in, in the catalog's own terms (such as weekday peak hours).
type CostMultiplier struct {
	Factor float64
	Window string
}

// AcceptsImages reports whether the model declares image input. A model with
// no modalities is text only, so this is false for anything Nova does not know.
func (m Model) AcceptsImages() bool {
	return slices.Contains(m.InputModalities, "image")
}

// SupportsDecisions reports whether the model is a decision model that speaks
// the Decisions API. A model with no output modalities is a chat model, so this
// is false for anything Nova does not know.
func (m Model) SupportsDecisions() bool {
	return slices.Contains(m.OutputModalities, "decisions")
}

// SupportsEmbeddings reports whether the model is an embedding model. The live
// roster marks one with the "embeddings" output modality; a model with no
// modalities is a chat model, so this is false for anything Nova does not know.
func (m Model) SupportsEmbeddings() bool {
	return slices.Contains(m.OutputModalities, "embeddings")
}

// SupportsSpeech reports whether the model is a text-to-speech model. The live
// roster marks one with the "speech" output modality; a model with no
// modalities is a chat model, so this is false for anything Nova does not know.
func (m Model) SupportsSpeech() bool {
	return slices.Contains(m.OutputModalities, "speech")
}

// SupportsTranscription reports whether the model is a speech-to-text model.
// The live roster marks one with the "transcription" output modality; a model
// with no modalities is a chat model, so this is false for anything Nova does
// not know.
func (m Model) SupportsTranscription() bool {
	return slices.Contains(m.OutputModalities, "transcription")
}

var (
	modelMu sync.RWMutex
	models  = map[string]Model{}
)

// RegisterModel adds a model to the catalog, replacing any entry with the same ID.
func RegisterModel(m Model) {
	if m.ID == "" {
		panic("provider: RegisterModel requires an ID")
	}
	modelMu.Lock()
	defer modelMu.Unlock()
	models[m.ID] = m
}

// GetModel returns a catalog entry by ID.
func GetModel(id string) (Model, bool) {
	modelMu.RLock()
	defer modelMu.RUnlock()
	m, ok := models[id]
	return m, ok
}

// Models returns the catalog sorted by provider then id.
func Models() []Model {
	modelMu.RLock()
	defer modelMu.RUnlock()
	out := slices.Collect(maps.Values(models))
	slices.SortFunc(out, func(a, b Model) int {
		return cmp.Or(cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.ID, b.ID))
	})
	return out
}
