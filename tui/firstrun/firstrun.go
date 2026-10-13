// Package firstrun is the flow a new user sees before the chat screen: log in
// or paste a key, then pick a model. The TUI drives the steps; Needed decides
// whether the flow runs at all.
package firstrun

import "github.com/AutumnsGrove/nova/config"

// Step is one screen in the first-run flow.
type Step int

const (
	// Login offers OpenRouter login or a pasted key.
	Login Step = iota
	// Model picks a model from the catalog.
	Model
	// Done ends the flow.
	Done
)

// Needed returns the steps to run. A resolvable key means no flow; a missing
// key runs Login then Model, so the flow runs again only while the key is
// missing (FRS08, FRS12).
func Needed(hasKey bool, cfg *config.Config) []Step {
	if hasKey {
		return nil
	}
	return []Step{Login, Model}
}
