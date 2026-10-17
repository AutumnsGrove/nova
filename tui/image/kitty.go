package image

import (
	"fmt"
	"strings"
)

// kittyChunk is the base64 chunk size the Kitty protocol allows.
const kittyChunk = 4096

// EncodeKitty returns the Kitty graphics sequence for base64 image data placed
// in cols by rows cells. C=1 leaves the cursor where it is, so the caller owns
// the line layout; a payload over 4096 characters is split with m=1
// continuations and a final m=0.
func EncodeKitty(data string, cols, rows int, id uint32) string {
	params := fmt.Sprintf("a=T,f=100,q=2,c=%d,r=%d,i=%d,C=1", cols, rows, id)
	if len(data) <= kittyChunk {
		return "\x1b_G" + params + ";" + data + "\x1b\\"
	}
	var b strings.Builder
	for first := true; len(data) > 0; first = false {
		chunk := data
		if len(chunk) > kittyChunk {
			chunk, data = data[:kittyChunk], data[kittyChunk:]
		} else {
			data = ""
		}
		last := len(data) == 0
		b.WriteString("\x1b_G")
		switch {
		case first:
			b.WriteString(params)
			b.WriteString(",m=1")
		case last:
			b.WriteString("m=0")
		default:
			b.WriteString("m=1")
		}
		b.WriteByte(';')
		b.WriteString(chunk)
		b.WriteString("\x1b\\")
	}
	return b.String()
}
