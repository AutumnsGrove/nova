package tool

import (
	"bytes"
	"strings"
	"testing"
)

func TestTailBuffer_KeepsHeadAndTail(t *testing.T) {
	b := NewTailBuffer(100)
	data := append(bytes.Repeat([]byte("a"), 90), bytes.Repeat([]byte("z"), 90)...)
	if n, err := b.Write(data); n != len(data) || err != nil {
		t.Fatalf("Write = %d, %v; want the full length and no error", n, err)
	}
	out := b.Bytes()
	if len(out) > 100+64 {
		t.Fatalf("output length %d exceeds the cap plus marker", len(out))
	}
	if !bytes.HasPrefix(out, bytes.Repeat([]byte("a"), 20)) {
		t.Fatalf("output should keep the first 20%%: %q", out[:20])
	}
	if !bytes.HasSuffix(out, bytes.Repeat([]byte("z"), 80)) {
		t.Fatal("output should keep the last 80%")
	}
	if !strings.Contains(string(out), "bytes omitted") {
		t.Fatalf("output should carry an omitted marker: %q", out)
	}
	if !b.Truncated() {
		t.Fatal("Truncated should be true")
	}
}

func TestTailBuffer_UnderCapIsVerbatim(t *testing.T) {
	b := NewTailBuffer(100)
	_, _ = b.Write([]byte("hello"))
	if got := string(b.Bytes()); got != "hello" {
		t.Fatalf("Bytes = %q, want hello", got)
	}
	if b.Truncated() {
		t.Fatal("Truncated should be false")
	}
}

func TestTailBuffer_NegativeLimitKeepsNothing(t *testing.T) {
	b := NewTailBuffer(-5)
	_, _ = b.Write([]byte("abc"))
	if !b.Truncated() || strings.Contains(string(b.Bytes()), "abc") {
		t.Fatalf("a negative limit should drop everything, got %q", b.Bytes())
	}
}
