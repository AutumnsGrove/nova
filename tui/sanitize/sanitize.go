//go:build !notui

// Package sanitize makes untrusted strings safe to draw in a terminal. Tool
// arguments, tool output, extension widgets and model text can all carry bytes
// a terminal acts on, and an approval prompt that shows them raw can be made to
// display something other than what will run.
package sanitize

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Text returns s with everything a terminal would act on removed: escape
// sequences, every control character except newline and tab (a carriage return
// or backspace redraws what came before it), and the Unicode characters that
// reorder or break the line that is drawn. Invalid UTF-8 is dropped. It keeps
// the visible text, including emoji joiners, as it was.
func Text(s string) string {
	// Strip first: it reads a raw 0x9b byte as an 8-bit CSI, which dropping invalid
	// UTF-8 would turn into plain text.
	s = strings.ToValidUTF8(ansi.Strip(s), "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || isBidiOrSeparator(r) {
			return -1
		}
		return r
	}, s)
}

// Line is Text for a name shown inside one line, such as a folder in a prompt:
// a newline or tab becomes a space, so the name cannot start a line of its own.
func Line(s string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(Text(s))
}

// isBidiOrSeparator reports whether r reorders text direction or ends a line in
// a way a terminal and a reader may disagree on.
func isBidiOrSeparator(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E: // embeddings and overrides
		return true
	case r >= 0x2066 && r <= 0x2069: // isolates
		return true
	}
	switch r {
	case 0x061C, 0x200E, 0x200F, 0x2028, 0x2029:
		return true
	}
	return false
}
