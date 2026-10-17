package media

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"golang.org/x/image/tiff"
)

// solid returns an RGBA image of the given size.
func solid(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	return img
}

// pngOf encodes an image as PNG, panicking on failure so fuzz seeds can use it.
func pngOf(w, h int) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, solid(w, h)); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// pngHeader builds a valid IHDR-only PNG claiming the given size, for the
// decompression-bomb guard.
func pngHeader(w, h uint32) []byte {
	var out bytes.Buffer
	out.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	data := make([]byte, 0, 13)
	data = binary.BigEndian.AppendUint32(data, w)
	data = binary.BigEndian.AppendUint32(data, h)
	data = append(data, 8, 6, 0, 0, 0) // bit depth 8, RGBA, deflate, adaptive, no interlace
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(data)))
	chunk.WriteString("IHDR")
	chunk.Write(data)
	_ = binary.Write(&chunk, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), data...)))
	out.Write(chunk.Bytes())
	return out.Bytes()
}

// dims decodes a part's base64 and reports its pixel size.
func dims(t *testing.T, part message.ContentPart) (int, int) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(part.Data)
	if err != nil {
		t.Fatalf("part data is not base64: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode normalized part: %v", err)
	}
	return cfg.Width, cfg.Height
}

func TestMedia_MED01_Sniff(t *testing.T) {
	var gifBuf bytes.Buffer
	if err := gif.Encode(&gifBuf, solid(2, 2), nil); err != nil {
		t.Fatal(err)
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, solid(2, 2), nil); err != nil {
		t.Fatal(err)
	}
	var tiffBuf bytes.Buffer
	if err := tiff.Encode(&tiffBuf, solid(2, 2), nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", pngOf(2, 2), "image/png"},
		{"jpeg", jpegBuf.Bytes(), "image/jpeg"},
		{"gif", gifBuf.Bytes(), "image/gif"},
		{"tiff", tiffBuf.Bytes(), "image/tiff"},
		{"text", []byte("not an image"), ""},
		{"empty", nil, ""},
	}
	for _, tc := range cases {
		got, ok := Sniff(tc.data)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: Sniff = %q, want false", tc.name, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%s: Sniff = %q, %v, want %q", tc.name, got, ok, tc.want)
		}
	}
}

func TestMedia_MED02_SmallPNG(t *testing.T) {
	res, err := Normalize(pngOf(2, 2), "image/png", "tiny.png")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if res.Part.Type != message.PartImage || res.Part.MimeType != "image/png" || res.Part.Name != "tiny.png" {
		t.Fatalf("part = %+v", res.Part)
	}
	if w, h := dims(t, res.Part); w != 2 || h != 2 {
		t.Errorf("dims = %dx%d, want 2x2", w, h)
	}
	if len(res.Hints) != 0 {
		t.Errorf("hints = %v, want none for an already-small PNG", res.Hints)
	}
}

func TestMedia_MED03_Resize(t *testing.T) {
	res, err := Normalize(pngOf(4000, 10), "image/png", "")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if w, h := dims(t, res.Part); w != 2000 || h != 5 {
		t.Errorf("dims = %dx%d, want 2000x5", w, h)
	}
	joined := strings.Join(res.Hints, "\n")
	if !strings.Contains(joined, "displayed at 2000x5") {
		t.Errorf("hints = %v, want a resize hint with the target size", res.Hints)
	}
}

func TestMedia_MED04_JPEGStaysLossy(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, solid(10, 10), nil); err != nil {
		t.Fatal(err)
	}
	res, err := Normalize(buf.Bytes(), "image/jpeg", "")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if res.Part.MimeType != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", res.Part.MimeType)
	}
}

func TestMedia_MED05_PixelBombRejected(t *testing.T) {
	_, err := Normalize(pngHeader(100_000, 100_000), "image/png", "")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestMedia_MED06_Orientation(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	src.Set(1, 0, color.RGBA{B: 0xff, A: 0xff})

	got := orient(src, 6)

	if b := got.Bounds(); b.Dx() != 1 || b.Dy() != 2 {
		t.Fatalf("bounds = %v, want 1x2", b)
	}
	top, topOK := got.At(0, 0).(color.RGBA)
	bottom, bottomOK := got.At(0, 1).(color.RGBA)
	if !topOK || !bottomOK || top.R != 0xff || top.B != 0 || bottom.B != 0xff || bottom.R != 0 {
		t.Errorf("pixels = top %v bottom %v, want red then blue", got.At(0, 0), got.At(0, 1))
	}
}
