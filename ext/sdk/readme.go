package sdk

import _ "embed"

//go:embed README.md
var markdown string

// Markdown returns the SDK's documentation, embedded so `nova docs sdk` can
// print it without the source tree.
func Markdown() string { return markdown }
