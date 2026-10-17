package clipboard

import (
	"context"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// readTimeout bounds one clipboard read, so a wedged clipboard cannot hang the
// TUI. It is a variable so a test can shorten it.
var readTimeout = 2 * time.Second

// commandReader runs a command and parses its stdout into PNG bytes.
type commandReader struct {
	name  string
	args  []string
	parse func([]byte) ([]byte, error)
}

// Read runs the command under the timeout. A missing binary is ErrUnavailable;
// a command that runs but yields no image is ErrNoImage.
func (r commandReader) Read(ctx context.Context) (Image, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, r.name, r.args...).Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Image{}, ErrUnavailable
		}
		return Image{}, ErrNoImage
	}
	data, perr := r.parse(out)
	if perr != nil || len(data) == 0 {
		return Image{}, ErrNoImage
	}
	return Image{Data: data, MimeType: "image/png", Name: "clipboard.png"}, nil
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
