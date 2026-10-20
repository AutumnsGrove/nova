package auth

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateBody_KeepsUTF8 checks an error message built from a long
// multi-byte response body is still valid UTF-8, because the cut must not land
// inside a character.
func TestTruncateBody_KeepsUTF8(t *testing.T) {
	// three-byte runes: byte 200 falls inside the 67th character.
	got := truncateBody([]byte(strings.Repeat("€", 100)))
	if !utf8.ValidString(got) {
		t.Fatalf("truncateBody produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncateBody = %q, want a truncation marker", got)
	}
}
