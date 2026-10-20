//go:build !notui

package render

import (
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/highlight"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Highlight syntax-highlights code and returns one line per input line, each
// within width. Control sequences in the code are removed by the highlighter.
func Highlight(code, filename string, st styles.Styles, width int) []string {
	color := useColor(st)
	lines := highlight.Lines(code, filename, color)
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
	}
	return lines
}

// highlightCode highlights a single line and returns plain text when colour is
// unavailable.
func highlightCode(code, filename string, st styles.Styles) string {
	return highlight.Line(code, filename, useColor(st))
}

// useColor reports whether the profile and theme should emit colours.
func useColor(st styles.Styles) bool {
	return st.Profile > colorprofile.ASCII && st.Theme.Name != "mono"
}
