package media

import (
	"bytes"
	"encoding/binary"
	"image/jpeg"
	"testing"
)

// withExif inserts an APP1 Exif segment holding one orientation tag right
// after the SOI of a JPEG. fill adds the 0xff padding byte JPEG allows before a
// marker.
func withExif(t *testing.T, jpegData []byte, order binary.ByteOrder, orientation uint16, fill bool) []byte {
	t.Helper()
	var tiff bytes.Buffer
	if order == binary.LittleEndian {
		tiff.WriteString("II")
	} else {
		tiff.WriteString("MM")
	}
	_ = binary.Write(&tiff, order, uint16(0x2a))
	_ = binary.Write(&tiff, order, uint32(8)) // IFD0 follows the 8-byte header
	_ = binary.Write(&tiff, order, uint16(1)) // one entry
	_ = binary.Write(&tiff, order, uint16(0x0112))
	_ = binary.Write(&tiff, order, uint16(3)) // SHORT
	_ = binary.Write(&tiff, order, uint32(1))
	_ = binary.Write(&tiff, order, orientation)
	_ = binary.Write(&tiff, order, uint16(0)) // pad the 4-byte value field
	_ = binary.Write(&tiff, order, uint32(0)) // no next IFD

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	seg := []byte{0xff, 0xe1}
	seg = binary.BigEndian.AppendUint16(seg, uint16(len(payload)+2))
	seg = append(seg, payload...)
	if fill {
		seg = append([]byte{0xff}, seg...)
	}
	return append(append(append([]byte{}, jpegData[:2]...), seg...), jpegData[2:]...)
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, solid(w, h), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMedia_EXF01_OrientationRead(t *testing.T) {
	base := encodeJPEG(t, 4, 2)
	for _, tc := range []struct {
		name  string
		order binary.ByteOrder
		fill  bool
	}{
		{"little endian", binary.LittleEndian, false},
		{"big endian", binary.BigEndian, false},
		{"fill byte before the marker", binary.LittleEndian, true},
	} {
		data := withExif(t, base, tc.order, 6, tc.fill)
		if got := exifOrientation(data, "image/jpeg"); got != 6 {
			t.Errorf("%s: orientation = %d, want 6", tc.name, got)
		}
	}
	if got := exifOrientation(base, "image/jpeg"); got != 1 {
		t.Errorf("no EXIF: orientation = %d, want 1", got)
	}
	if got := exifOrientation(withExif(t, base, binary.BigEndian, 9, false), "image/jpeg"); got != 1 {
		t.Errorf("out-of-range tag: orientation = %d, want 1", got)
	}
	if got := exifOrientation(withExif(t, base, binary.BigEndian, 6, false), "image/png"); got != 1 {
		t.Errorf("non-JPEG: orientation = %d, want 1", got)
	}
}

// A segment whose declared length runs past the data must stop the scan, not
// read out of range.
func TestMedia_EXF02_TruncatedSegment(t *testing.T) {
	data := withExif(t, encodeJPEG(t, 4, 2), binary.BigEndian, 6, false)
	for cut := 2; cut < 40; cut++ {
		_ = exifOrientation(data[:cut], "image/jpeg") // must not panic
	}
	if got := exifOrientation(data[:20], "image/jpeg"); got != 1 {
		t.Errorf("truncated: orientation = %d, want 1", got)
	}
}

// End to end: a 4x2 photo tagged "rotate 90" is stored 2x4.
func TestMedia_EXF03_NormalizeAppliesOrientation(t *testing.T) {
	data := withExif(t, encodeJPEG(t, 4, 2), binary.BigEndian, 6, false)
	res, err := Normalize(data, "image/jpeg", "p.jpg")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if w, h := dims(t, res.Part); w != 2 || h != 4 {
		t.Errorf("dims = %dx%d, want 2x4", w, h)
	}
}
