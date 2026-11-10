package voice

import (
	"strings"
	"unicode"
)

// ChunkRunes is the default chunk size: long enough to cover a couple of short
// sentences, short enough that the first chunk synthesizes quickly. It is also
// far below most models' input caps, which is the point.
const ChunkRunes = 300

// Chunks splits text into sentence-sized pieces of at most maxRunes runes. A
// sentence longer than the cap is split at the last space that fits, and a word
// longer than the cap is hard-split. It never splits a rune and never returns an
// empty chunk. Blank input returns nil. A maxRunes of zero or less means
// ChunkRunes.
func Chunks(text string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = ChunkRunes
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var out []string
	for _, sentence := range sentences(text) {
		out = append(out, splitRunes(sentence, maxRunes)...)
	}
	return out
}

// sentences cuts text after a run of . ! or ? that is followed by whitespace or
// the end. It is a heuristic, not a tokenizer: "Dr. Smith" splits early, which
// only changes where the audio pauses, not what is said.
func sentences(text string) []string {
	r := []rune(text)
	var out []string
	start := 0
	for i := 0; i < len(r); i++ {
		if r[i] != '.' && r[i] != '!' && r[i] != '?' {
			continue
		}
		end := i + 1
		for end < len(r) && (r[end] == '.' || r[end] == '!' || r[end] == '?') {
			end++
		}
		if end < len(r) && !unicode.IsSpace(r[end]) {
			// A decimal point or an abbreviation with no space after it: keep
			// scanning this sentence.
			i = end - 1
			continue
		}
		if s := strings.TrimSpace(string(r[start:end])); s != "" {
			out = append(out, s)
		}
		start = end
		i = end - 1
	}
	if s := strings.TrimSpace(string(r[start:])); s != "" {
		out = append(out, s)
	}
	return out
}

// splitRunes cuts one sentence into pieces of at most maxRunes, preferring a
// break at a space and falling back to a hard split for one longer word.
func splitRunes(sentence string, maxRunes int) []string {
	r := []rune(sentence)
	var out []string
	for len(r) > maxRunes {
		cut := maxRunes
		if cut < len(r) && !unicode.IsSpace(r[cut]) {
			// No break exactly at the cap, so back up to the last space that
			// fits. A single word longer than the cap has none and is cut hard.
			for i := cut - 1; i > 0; i-- {
				if unicode.IsSpace(r[i]) {
					cut = i
					break
				}
			}
		}
		if piece := strings.TrimSpace(string(r[:cut])); piece != "" {
			out = append(out, piece)
		}
		r = r[cut:]
		for len(r) > 0 && unicode.IsSpace(r[0]) {
			r = r[1:]
		}
	}
	if piece := strings.TrimSpace(string(r)); piece != "" {
		out = append(out, piece)
	}
	return out
}
