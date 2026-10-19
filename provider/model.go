package provider

import (
	"sort"
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
	// ImplicitCaching reports that the provider caches a repeated request
	// prefix on its own, so Nova must not send an explicit cache marker.
	// False is the safe default: an anthropic-style model needs the marker to
	// cache at all, and a model the catalog does not know is never marked.
	ImplicitCaching bool
}

// AcceptsImages reports whether the model declares image input. A model with
// no modalities is text only, so this is false for anything Nova does not know.
func (m Model) AcceptsImages() bool {
	for _, kind := range m.InputModalities {
		if kind == "image" {
			return true
		}
	}
	return false
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
	out := make([]Model, 0, len(models))
	for _, m := range models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].ID < out[j].ID
	})
	return out
}
