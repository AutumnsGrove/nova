package bash

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

// tailCapacity is the number of trailing bytes a buffer of the given limit keeps.
func tailCapacity(limit int) int {
	return limit - limit*20/100
}

func TestTailBuffer_KeepsHeadAndTail(t *testing.T) {
	b := newTailBuffer(100)
	data := append(bytes.Repeat([]byte("a"), 90), bytes.Repeat([]byte("z"), 90)...)
	b.write(data)
	out := b.bytes()
	if len(out) > 100+64 {
		t.Fatalf("output length %d exceeds the cap plus marker", len(out))
	}
	if !bytes.HasPrefix(out, bytes.Repeat([]byte("a"), 20)) {
		t.Fatalf("output should keep the first 20%%: %q", out[:20])
	}
	if !bytes.HasSuffix(out, bytes.Repeat([]byte("z"), 80)) {
		t.Fatalf("output should keep the last 80%%")
	}
	if !strings.Contains(string(out), "bytes omitted") {
		t.Fatalf("output should carry an omitted marker: %q", out)
	}
	if !b.truncated() {
		t.Fatal("truncated should be true")
	}
}

func TestTailBuffer_UnderCapIsVerbatim(t *testing.T) {
	b := newTailBuffer(100)
	b.write([]byte("hello"))
	if got := string(b.bytes()); got != "hello" {
		t.Fatalf("bytes = %q, want hello", got)
	}
	if b.truncated() {
		t.Fatal("truncated should be false")
	}
}

func TestSanitizeOutput_CarriageReturns(t *testing.T) {
	if got := sanitizeOutput([]byte("a\rb\rc\n")); got != "c\n" {
		t.Fatalf("got %q, want c\\n", got)
	}
	if got := sanitizeOutput([]byte("a\r\nb\r\n")); got != "a\nb\n" {
		t.Fatalf("got %q, want a\\nb\\n", got)
	}
}

func TestSanitizeOutput_NULAndInvalid(t *testing.T) {
	got := sanitizeOutput([]byte{'a', 0, 'b', 0xff, 'c'})
	if strings.ContainsRune(got, 0) {
		t.Fatalf("got %q, NUL should be replaced", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("got %q, want valid UTF-8", got)
	}
	if !strings.Contains(got, "\uFFFD") {
		t.Fatalf("got %q, want a replacement character", got)
	}
}

func FuzzOutputBuffer(f *testing.F) {
	f.Add([]byte("hello world"), 8)
	f.Add([]byte("abc\rdef\rghi\n"), 4)
	f.Add([]byte{}, 0)
	f.Fuzz(func(t *testing.T, data []byte, limit int) {
		if limit < 0 {
			limit = -limit
		}
		if limit > 1<<20 {
			limit = 1 << 20
		}
		b := newTailBuffer(limit)
		b.write(data)
		out := b.bytes()
		if len(out) > limit+64 {
			t.Fatalf("output length %d exceeds cap %d plus marker", len(out), limit)
		}
		if len(data) <= limit {
			if !bytes.Equal(out, data) {
				t.Fatalf("under cap: got %q, want %q", out, data)
			}
		} else if tail := tailCapacity(limit); tail > 0 {
			if !bytes.HasSuffix(out, data[len(data)-tail:]) {
				t.Fatalf("output does not end with the last %d bytes", tail)
			}
		}
		// Byte-by-byte writes must produce the same result as one write.
		step := newTailBuffer(limit)
		for _, c := range data {
			step.write([]byte{c})
		}
		if !bytes.Equal(out, step.bytes()) {
			t.Fatalf("chunked write differs from one write")
		}
	})
}

func FuzzSanitizeOutput(f *testing.F) {
	f.Add([]byte("a\x00b\xffc\rd\n"))
	f.Add([]byte("\r\n\r"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got := sanitizeOutput(data)
		if !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8 result for %q", data)
		}
		if strings.ContainsRune(got, 0) {
			t.Fatalf("NUL survived in %q", got)
		}
	})
}
