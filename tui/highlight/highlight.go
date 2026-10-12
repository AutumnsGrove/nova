//go:build !notui

// Package highlight turns source code into ANSI-styled lines with chroma. It
// is shared by tui/diff and tui/render so neither has to duplicate the lexer
// and formatter setup.
package highlight

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Lines highlights code and returns one string per input line. When color is
// false, or the code is empty, the input is returned unchanged. A formatting
// error falls back to the plain input, so the caller never loses text.
func Lines(code, filename string, color bool) []string {
	in := strings.Split(code, "\n")
	if !color || code == "" {
		return in
	}
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return in
	}
	var b bytes.Buffer
	if err := formatter.Format(&b, style, it); err != nil {
		return in
	}
	out := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for len(out) < len(in) {
		out = append(out, "")
	}
	return out[:len(in)]
}

// Line highlights a single line of code. It is the cheap path for a diff line,
// which has no cross-line context to preserve.
func Line(code, filename string, color bool) string {
	return Lines(code, filename, color)[0]
}
