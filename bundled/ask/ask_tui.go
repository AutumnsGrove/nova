//go:build !notui

package ask

import "fmt"

// Card renders a one-line summary of the questions for the TUI tool card. It
// lives in a build-tagged file so the notui build keeps only the tool logic.
func Card(questions int) string {
	return fmt.Sprintf("asked %d question(s)", questions)
}
