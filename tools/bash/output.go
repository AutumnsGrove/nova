package bash

import (
	"bytes"
	"strings"
)

// sanitizeOutput makes captured bytes safe to show: carriage-return progress
// bars are collapsed, and NUL bytes and invalid UTF-8 become U+FFFD.
func sanitizeOutput(p []byte) string {
	p = collapseCR(p)
	p = bytes.ReplaceAll(p, []byte{0}, []byte("\uFFFD"))
	return strings.ToValidUTF8(string(p), "\uFFFD")
}

// collapseCR keeps only the text after the last carriage return on each line,
// while treating `\r\n` as a plain line break.
func collapseCR(p []byte) []byte {
	lines := bytes.Split(p, []byte{'\n'})
	for i, line := range lines {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if j := bytes.LastIndexByte(line, '\r'); j >= 0 {
			line = line[j+1:]
		}
		lines[i] = line
	}
	return bytes.Join(lines, []byte{'\n'})
}
