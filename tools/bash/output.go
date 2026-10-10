package bash

import (
	"bytes"
	"fmt"
	"strings"
)

// tailBuffer keeps at most a fixed number of output bytes, biased to the tail:
// the first 20% in arrival order and the last 80%. Memory never grows with the
// amount of output a command produces.
type tailBuffer struct {
	headCap, tailCap int
	head, tail       []byte
	total            int
}

// newTailBuffer builds a buffer capped at limit bytes.
func newTailBuffer(limit int) *tailBuffer {
	if limit < 0 {
		limit = 0
	}
	head := limit * 20 / 100
	return &tailBuffer{headCap: head, tailCap: limit - head}
}

// write appends one chunk, keeping only the head and tail slices.
func (b *tailBuffer) write(p []byte) {
	b.total += len(p)
	if n := b.headCap - len(b.head); n > 0 {
		if n > len(p) {
			n = len(p)
		}
		b.head = append(b.head, p[:n]...)
		p = p[n:]
	}
	if b.tailCap <= 0 || len(p) == 0 {
		return
	}
	b.tail = append(b.tail, p...)
	// Re-slicing keeps the backing array bounded to about twice the cap,
	// because append reuses it until it is full and then reallocates.
	if len(b.tail) > b.tailCap {
		b.tail = b.tail[len(b.tail)-b.tailCap:]
	}
}

// bytes returns the captured output with a marker where data was dropped. The
// result is at most the cap plus the marker and is safe to show.
func (b *tailBuffer) bytes() []byte {
	out := append([]byte{}, b.head...)
	if omitted := b.total - len(b.head) - len(b.tail); omitted > 0 {
		out = append(out, fmt.Sprintf("\n... (%d bytes omitted) ...\n", omitted)...)
	}
	return append(out, b.tail...)
}

// truncated reports whether bytes drops anything.
func (b *tailBuffer) truncated() bool { return b.total > len(b.head)+len(b.tail) }

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
