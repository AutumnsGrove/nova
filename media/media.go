// Package media normalizes image bytes on their way into the transcript. It
// decodes the formats a provider accepts, applies EXIF orientation, bounds the
// decoded pixel count before allocating, resizes to a longest edge, re-encodes
// under a byte cap, and returns a message.ContentPart ready to store in a
// session or send to a model. It is the one place an image is made safe, so a
// second caller cannot forget a limit.
package media

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // registers the GIF decoder with image.Decode
	"image/jpeg"
	"image/png"

	"github.com/AutumnsGrove/nova/message"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" // registers the TIFF decoder
	_ "golang.org/x/image/webp" // registers the WebP decoder
)

const (
	// MaxEdge is the longest edge a stored image is resized to, pi's default.
	MaxEdge = 2000
	// MaxBytes is the encoded byte cap for one image part, pi's 4.5 MiB.
	MaxBytes = 4500 * 1024
	// MaxPixels bounds a decoded image before it is allocated, so a
	// decompression bomb is refused instead of exhausting memory.
	MaxPixels = 80_000_000
	// JPEGQuality is the re-encode quality for a lossy source.
	JPEGQuality = 85
)

// Errors a caller can match.
var (
	ErrUnsupported = errors.New("media: unsupported image format")
	ErrTooLarge    = errors.New("media: image is too large")
)

// Result is a normalized part plus the notes the caller should fold into the
// text it sends the model, such as a conversion or resize hint.
type Result struct {
	Part  message.ContentPart
	Hints []string
}

// mimeByFormat maps image.DecodeConfig's format name to a mime type. It lists
// exactly the formats whose decoders this package imports, so a decoder that
// another package happens to register is still refused.
var mimeByFormat = map[string]string{
	"png":  "image/png",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",
	"webp": "image/webp",
	"tiff": "image/tiff",
}

// Sniff reports the mime type of a supported image without decoding it fully.
func Sniff(data []byte) (string, bool) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return "", false
	}
	mime, ok := mimeByFormat[format]
	return mime, ok
}

// Dimensions reports an encoded image's pixel size without decoding it fully,
// for a renderer that only needs the aspect ratio.
func Dimensions(data []byte) (w, h int, ok bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// PNG returns data as a PNG, for a terminal that can draw only that format. A
// PNG comes back untouched; anything else is decoded and re-encoded, so it
// costs a decode and should be done once per image shown, never per frame.
func PNG(data []byte) ([]byte, error) {
	if mime, ok := Sniff(data); !ok {
		return nil, ErrUnsupported
	} else if mime == "image/png" {
		return data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	out, _, err := encode(img, false)
	return out, err
}

// Normalize decodes data, applies EXIF orientation, resizes it to fit the
// limits, and re-encodes it under MaxBytes. The mime type a caller claims is
// ignored (the decoded format decides how it is re-encoded); the parameter stays
// so callers need not change. name is display only.
func Normalize(data []byte, _ /* claimed mime type */, name string) (Result, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	mime, ok := mimeByFormat[format]
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrUnsupported, format)
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return Result{}, fmt.Errorf("%w: %dx%d pixels", ErrTooLarge, cfg.Width, cfg.Height)
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	oriented := orient(src, exifOrientation(data, mime))
	origW, origH := oriented.Bounds().Dx(), oriented.Bounds().Dy()

	targetW, targetH, resized := fit(origW, origH, MaxEdge)
	scaled := maybeScale(oriented, targetW, targetH)
	lossy := mime == "image/jpeg" || mime == "image/webp" || mime == "image/tiff"
	encoded, encMime, err := encode(scaled, lossy)
	if err != nil {
		return Result{}, err
	}
	// A re-encode can still be over the cap; shrink in steps rather than refuse
	// an image that has a smaller valid form.
	for len(encoded) > MaxBytes && targetW > 1 && targetH > 1 {
		targetW, targetH = max(1, targetW*3/4), max(1, targetH*3/4)
		resized = true
		scaled = maybeScale(oriented, targetW, targetH)
		encoded, encMime, err = encode(scaled, lossy)
		if err != nil {
			return Result{}, err
		}
	}
	if len(encoded) > MaxBytes {
		return Result{}, fmt.Errorf("%w: still %d bytes after resize", ErrTooLarge, len(encoded))
	}

	var hints []string
	if encMime != mime {
		hints = append(hints, fmt.Sprintf("[Image converted from %s to %s.]", mime, encMime))
	}
	if resized {
		hints = append(hints, fmt.Sprintf(
			"[Image: original %dx%d, displayed at %dx%d. Multiply coordinates by %.2f to map to the original image.]",
			origW, origH, targetW, targetH, float64(origW)/float64(targetW)))
	}
	part := message.ContentPart{
		Type:     message.PartImage,
		MimeType: encMime,
		Data:     base64.StdEncoding.EncodeToString(encoded),
		Name:     name,
	}
	return Result{Part: part, Hints: hints}, nil
}

// fit returns the dimensions that bring an image within edge, preserving the
// aspect ratio. resized is false when the image already fits.
func fit(w, h, edge int) (int, int, bool) {
	if w <= edge && h <= edge {
		return w, h, false
	}
	if w >= h {
		return edge, max(1, h*edge/w), true
	}
	return max(1, w*edge/h), edge, true
}

// maybeScale returns src at the target size, or src itself when it is already
// there.
func maybeScale(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	if b.Dx() == w && b.Dy() == h {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// encode re-encodes img; lossy picks JPEG, otherwise PNG. It returns the bytes
// and the mime type written.
func encode(img image.Image, lossy bool) ([]byte, string, error) {
	var buf bytes.Buffer
	if lossy {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
			return nil, "", fmt.Errorf("media: encode jpeg: %w", err)
		}
		return buf.Bytes(), "image/jpeg", nil
	}
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", fmt.Errorf("media: encode png: %w", err)
	}
	return buf.Bytes(), "image/png", nil
}
