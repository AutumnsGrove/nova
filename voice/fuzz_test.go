package voice

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzChunks_A30 checks the properties a caller depends on: a chunk is never
// empty, never splits a rune, and never exceeds the cap. Every chunk is also a
// slice of the original text, so no character is invented or reordered.
func FuzzChunks_A30(f *testing.F) {
	f.Add("One. Two! Three?", 5)
	f.Add("no terminator here", 300)
	f.Add("é. é!", 1)
	f.Add("", 0)
	f.Add("....", -3)
	f.Fuzz(func(t *testing.T, text string, max int) {
		cap := max
		if cap <= 0 {
			cap = ChunkRunes
		}
		validInput := utf8.ValidString(text)
		prev := 0
		for i, chunk := range Chunks(text, max) {
			if chunk == "" {
				t.Fatalf("chunk %d is empty", i)
			}
			if !utf8.ValidString(chunk) {
				t.Fatalf("chunk %d is not valid UTF-8: %q", i, chunk)
			}
			if n := utf8.RuneCountInString(chunk); n > cap {
				t.Fatalf("chunk %d is %d runes, over the cap %d", i, n, cap)
			}
			if !validInput {
				continue
			}
			at := strings.Index(text[prev:], chunk)
			if at < 0 {
				t.Fatalf("chunk %d %q is not a slice of the text after byte %d", i, chunk, prev)
			}
			prev += at + len(chunk)
		}
	})
}
