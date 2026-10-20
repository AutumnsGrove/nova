package clipboard

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"os/exec"
	"strings"
	"time"
)

// readTimeout bounds one clipboard read, so a wedged clipboard cannot hang the
// TUI. It is a variable so a test can shorten it.
var readTimeout = 2 * time.Second

// maxOutput caps what one read keeps from a tool. It is a variable so a test
// can lower it.
var maxOutput = 64 << 20

// commandReader runs a command and parses its stdout into PNG bytes.
type commandReader struct {
	name  string
	args  []string
	parse func([]byte) ([]byte, error)
}

// pipeGrace bounds how long Read waits for the output pipe after the command is
// killed. A tool that forked a helper leaves the pipe open, and without it Read
// would block until the helper exits.
const pipeGrace = 200 * time.Millisecond

// Read runs the command under the timeout. A missing binary is ErrUnavailable;
// a command that runs but yields no image, or times out, is ErrNoImage; output
// over maxOutput is ErrTooLarge; a cancelled caller gets its own context error.
func (r commandReader) Read(ctx context.Context) (Image, error) {
	runCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	out := &cappedBuffer{limit: maxOutput}
	cmd := exec.CommandContext(runCtx, r.name, r.args...)
	cmd.Stdout = out
	cmd.WaitDelay = pipeGrace
	err := cmd.Run()
	switch {
	case out.exceeded:
		return Image{}, ErrTooLarge
	case ctx.Err() != nil:
		return Image{}, ctx.Err()
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return Image{}, ErrUnavailable
	case err != nil:
		return Image{}, ErrNoImage
	}
	data, perr := r.parse(out.buf.Bytes())
	if perr != nil || len(data) == 0 {
		return Image{}, ErrNoImage
	}
	return Image{Data: data, MimeType: "image/png", Name: "clipboard.png"}, nil
}

// cappedBuffer keeps at most limit bytes. Past that it fails the write, which
// stops the command, and remembers that it did.
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.limit {
		c.exceeded = true
		return 0, errors.New("clipboard: output over the cap")
	}
	return c.buf.Write(p)
}

// parseAppleScriptData decodes `«data PNGf<hex>»`, the shape osascript prints
// for `the clipboard as «class PNGf»`.
func parseAppleScriptData(out []byte) ([]byte, error) {
	const prefix = "«data PNGf"
	s := strings.TrimSpace(string(out))
	i := strings.Index(s, prefix)
	if i < 0 {
		return nil, ErrNoImage
	}
	s = s[i+len(prefix):]
	j := strings.Index(s, "»")
	if j < 0 {
		return nil, ErrNoImage
	}
	return hex.DecodeString(strings.TrimSpace(s[:j]))
}

// parseRawPNG returns the bytes unchanged; the Linux tools already print PNG.
func parseRawPNG(out []byte) ([]byte, error) { return out, nil }
