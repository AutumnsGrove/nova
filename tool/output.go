package tool

import "fmt"

// TailBuffer keeps at most a fixed number of output bytes, biased to the tail:
// the first 20% in arrival order and the last 80%. Memory never grows with the
// amount of output a command produces. Bash and extension commands share it so
// both shape their output the same way.
type TailBuffer struct {
	headCap, tailCap int
	head, tail       []byte
	total            int
}

// NewTailBuffer builds a buffer capped at limit bytes.
func NewTailBuffer(limit int) *TailBuffer {
	limit = max(limit, 0)
	head := limit * 20 / 100
	return &TailBuffer{headCap: head, tailCap: limit - head}
}

// Write appends one chunk, keeping only the head and tail slices. It never
// fails, so it is safe as a process output sink.
func (b *TailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.total += n
	if keep := min(b.headCap-len(b.head), len(p)); keep > 0 {
		b.head = append(b.head, p[:keep]...)
		p = p[keep:]
	}
	if b.tailCap <= 0 || len(p) == 0 {
		return n, nil
	}
	b.tail = append(b.tail, p...)
	// Re-slicing keeps the backing array bounded to about twice the cap,
	// because append reuses it until it is full and then reallocates.
	if len(b.tail) > b.tailCap {
		b.tail = b.tail[len(b.tail)-b.tailCap:]
	}
	return n, nil
}

// Bytes returns the captured output with a marker where data was dropped. The
// result is at most the cap plus the marker.
func (b *TailBuffer) Bytes() []byte {
	out := append([]byte{}, b.head...)
	if omitted := b.total - len(b.head) - len(b.tail); omitted > 0 {
		out = append(out, fmt.Sprintf("\n... (%d bytes omitted) ...\n", omitted)...)
	}
	return append(out, b.tail...)
}

// Truncated reports whether Bytes drops anything.
func (b *TailBuffer) Truncated() bool { return b.total > len(b.head)+len(b.tail) }
