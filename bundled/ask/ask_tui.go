//go:build !notui

package ask

import (
	"fmt"
	"strings"
)

// Card renders a one-line summary of a call's questions for the TUI tool card.
// labels are display-ready names, one per question. It lives in a build-tagged
// file so the notui build keeps only the tool logic.
func Card(labels []string) string {
	if len(labels) == 0 {
		return "no questions"
	}
	noun := "questions"
	if len(labels) == 1 {
		noun = "question"
	}
	return fmt.Sprintf("%d %s (%s)", len(labels), noun, strings.Join(labels, ", "))
}
