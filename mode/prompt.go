package mode

import _ "embed"

// basePrompt is the generic system prompt prepended to every session. It is
// short on purpose: project instructions, skills, and tools add the specifics.
//
//go:embed prompt.md
var basePrompt string
