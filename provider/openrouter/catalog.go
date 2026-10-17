package openrouter

import "github.com/AutumnsGrove/nova/provider"

// DefaultModel is the stable id of the model Nova selects when nothing else is
// configured.
const DefaultModel = "deepseek/deepseek-v4.1"

// Catalog is the code-defined set of OpenRouter models Nova ships with. It is
// deliberately small: a stable id keeps a stored choice resolving when a
// provider renames a slug, and the listed price gives cost accounting a
// fallback when a response reports no cost.
//
// IDs, slugs, context windows, and per-million-token prices were taken from
// OpenRouter's model list (GET https://openrouter.ai/api/v1/models) on
// 2026-10-09. Prices are in US dollars per million tokens.
// textAndImage marks a model that accepts image input, mirroring
// OpenRouter's input_modalities for the entries below.
var textAndImage = []string{"text", "image"}

var Catalog = []provider.Model{
	{
		ID:            DefaultModel,
		Name:          "DeepSeek V4.1 Flash",
		Provider:      "openrouter",
		Slug:          "deepseek/deepseek-v4.1-flash",
		ContextWindow: 1_048_576,
		InPerM:        0.3,
		OutPerM:       1.2,
	},
	{
		ID:              "anthropic/claude-opus-5.5",
		Name:            "Claude Opus 5.5",
		Provider:        "openrouter",
		Slug:            "anthropic/claude-opus-5.5",
		ContextWindow:   1_000_000,
		InPerM:          4,
		OutPerM:         20,
		InputModalities: textAndImage,
	},
	{
		ID:              "anthropic/claude-haiku-5.5",
		Name:            "Claude Haiku 5.5",
		Provider:        "openrouter",
		Slug:            "anthropic/claude-haiku-5.5",
		ContextWindow:   1_000_000,
		InPerM:          0.1,
		OutPerM:         0.5,
		InputModalities: textAndImage,
	},
	{
		ID:              "openai/gpt-6.1-sol",
		Name:            "GPT-6.1 Sol",
		Provider:        "openrouter",
		Slug:            "openai/gpt-6.1-sol",
		ContextWindow:   1_050_000,
		InPerM:          2,
		OutPerM:         10,
		InputModalities: textAndImage,
	},
	{
		ID:              "openai/gpt-6-luna",
		Name:            "GPT-6 Luna",
		Provider:        "openrouter",
		Slug:            "openai/gpt-6-luna",
		ContextWindow:   1_050_000,
		InPerM:          0.1,
		OutPerM:         0.5,
		InputModalities: textAndImage,
	},
}

// RegisterModels adds Catalog to the process-wide provider catalog, replacing
// any entry with the same id. It is called by init, so importing the package
// makes the models available; tests may call it to restore the state.
func RegisterModels() {
	for _, m := range Catalog {
		provider.RegisterModel(m)
	}
}

func init() { RegisterModels() }
