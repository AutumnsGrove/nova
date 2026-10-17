package media

import (
	"encoding/base64"
	"testing"
)

// FuzzNormalize_MED07 feeds the normalizer arbitrary bytes: it must never
// panic, and any part it returns must be base64 that decodes under the byte
// cap, because that part is what goes into the session and the request.
func FuzzNormalize_MED07(f *testing.F) {
	f.Add(pngOf(4, 4), "image/png")
	f.Add(pngHeader(100_000, 100_000), "image/png")
	f.Add([]byte("not an image"), "image/png")
	f.Add([]byte{}, "")
	f.Fuzz(func(t *testing.T, data []byte, mime string) {
		res, err := Normalize(data, mime, "fuzz")
		if err != nil {
			return
		}
		raw, decErr := base64.StdEncoding.DecodeString(res.Part.Data)
		if decErr != nil {
			t.Fatalf("part data is not base64: %v", decErr)
		}
		if len(raw) > MaxBytes {
			t.Fatalf("part is %d bytes, over the cap %d", len(raw), MaxBytes)
		}
		if res.Part.MimeType == "" {
			t.Fatal("part has no mime type")
		}
	})
}
