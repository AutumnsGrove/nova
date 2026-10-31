//go:build !notui

// Package sanitize makes untrusted strings safe to draw in a terminal. Tool
// arguments, tool output, extension widgets and model text can all carry bytes
// a terminal acts on, and an approval prompt that shows them raw can be made to
// display something other than what will run.
package sanitize

import (
	"strings"
	"unicode"
	"unicode/utf8"

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

// Styled is Text for a line that already carries trusted styling: it keeps
// SGR sequences (colour, bold, faint, reverse) so a rendered line survives
// clipping, and drops every other escape sequence and control character. A
// caller must have cleaned its untrusted text already, or run it through Text
// first; Styled exists so the clipping in a dialog does not throw away the
// styles the dialog built.
func Styled(s string) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n := seqLen(s[i:])
			if ok := n > 0 && s[i+n-1] == 'm' && n > 2 && s[i+1] == '['; ok {
				b.WriteString(s[i : i+n])
			}
			i += max(n, 1)
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == utf8.RuneError && size == 1 {
			continue
		}
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || isBidiOrSeparator(r)) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// seqLen reports the length of the escape sequence at the start of s, or 0.
func seqLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 0
	}
	switch s[1] {
	case '[':
		i := 2
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
			i++
		}
		if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
		return i
	case ']', 'P', '^', '_', 'X': // OSC, DCS, PM, APC, SOS: end at BEL or ST
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	default: // ESC with intermediates and one final byte
		i := 1
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) {
			return i + 1
		}
		return i
	}
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
