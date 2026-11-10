package voice

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunks_A20(t *testing.T) {
	cases := []struct {
		name string
		text string
		max  int
		want []string
	}{
		{"blank", "   \n\t ", 300, nil},
		{"empty", "", 300, nil},
		{"one short sentence", "Hello there.", 300, []string{"Hello there."}},
		{"three sentences", "One. Two! Three?", 300, []string{"One.", "Two!", "Three?"}},
		{"no terminator", "Just some words", 300, []string{"Just some words"}},
		{"decimal does not split", "Pi is 3.14 and change.", 300, []string{"Pi is 3.14 and change."}},
		{"newline separates", "First.\nSecond.", 300, []string{"First.", "Second."}},
		{"exactly at the cap", "abcd", 4, []string{"abcd"}},
		{"long sentence splits at a space", "aaa bbb ccc ddd", 7, []string{"aaa bbb", "ccc ddd"}},
		{"one long word is hard-split", strings.Repeat("x", 10), 4, []string{"xxxx", "xxxx", "xx"}},
		{"trailing space is dropped", "  One.  Two.  ", 300, []string{"One.", "Two."}},
	}
	for _, tc := range cases {
		got := Chunks(tc.text, tc.max)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: Chunks() = %q, want %q", tc.name, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: chunk %d = %q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

func TestChunks_A20_RespectsTheCapAndRunes(t *testing.T) {
	// A string of multi-byte runes with no spaces: every chunk must stay under
	// the cap and remain valid UTF-8, so no split lands inside a rune.
	text := strings.Repeat("é", 25)
	for _, max := range []int{1, 2, 3, 7, 24} {
		for i, chunk := range Chunks(text, max) {
			if n := utf8.RuneCountInString(chunk); n > max {
				t.Errorf("max %d: chunk %d is %d runes", max, i, n)
			}
			if !utf8.ValidString(chunk) {
				t.Errorf("max %d: chunk %d is not valid UTF-8", max, i)
			}
		}
	}
}

func TestChunks_A20_DefaultCap(t *testing.T) {
	text := strings.Repeat("word ", 200)
	for i, chunk := range Chunks(text, 0) {
		if n := utf8.RuneCountInString(chunk); n > ChunkRunes {
			t.Errorf("chunk %d is %d runes, over the default cap %d", i, n, ChunkRunes)
		}
	}
}

func TestChunks_A20_NeverEmptyOrBlank(t *testing.T) {
	for _, text := range []string{"...", "!?", "  .  .  ", "\n\n"} {
		for _, chunk := range Chunks(text, 300) {
			if strings.TrimSpace(chunk) == "" {
				t.Errorf("Chunks(%q) produced a blank chunk", text)
			}
		}
	}
}
