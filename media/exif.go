package media

import (
	"encoding/binary"
	"image"
)

// exifOrientation returns the EXIF orientation (1..8) of a JPEG, or 1 when it
// is absent or unreadable. Go's image/jpeg ignores EXIF, so without this a
// phone photo is described and rendered sideways.
func exifOrientation(data []byte, mime string) int {
	if mime != "image/jpeg" || len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	offset := 2
	for offset+4 <= len(data) {
		if data[offset] != 0xff {
			offset++
			continue
		}
		marker := data[offset+1]
		if marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			offset += 2
			continue
		}
		if marker == 0xda || marker == 0xd9 {
			return 1 // start of scan or end of image: no EXIF ahead
		}
		size := int(data[offset+2])<<8 | int(data[offset+3])
		if size < 2 || offset+2+size > len(data) {
			return 1
		}
		if marker == 0xe1 {
			if o, ok := exifOrientationFromAPP1(data[offset+4 : offset+2+size]); ok {
				return o
			}
		}
		offset += 2 + size
	}
	return 1
}

// exifOrientationFromAPP1 reads the orientation tag from an APP1 payload that
// begins with the Exif header and a TIFF block.
func exifOrientationFromAPP1(payload []byte) (int, bool) {
	if len(payload) < 6 || string(payload[:6]) != "Exif\x00\x00" {
		return 0, false
	}
	tiff := payload[6:]
	if len(tiff) < 8 {
		return 0, false
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, false
	}
	if order.Uint16(tiff[2:4]) != 0x2a {
		return 0, false
	}
	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0, false
	}
	count := int(order.Uint16(tiff[ifd : ifd+2]))
	entry := ifd + 2
	for i := 0; i < count; i++ {
		if entry+12 > len(tiff) {
			return 0, false
		}
		e := tiff[entry : entry+12]
		if order.Uint16(e[0:2]) == 0x0112 {
			val := int(order.Uint16(e[8:10]))
			if val >= 1 && val <= 8 {
				return val, true
			}
			return 0, false
		}
		entry += 12
	}
	return 0, false
}

// orient applies an EXIF orientation to src. Orientations 5 to 8 swap width and
// height. Anything outside 1..8, or 1 itself, returns src unchanged.
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	W, H := b.Dx(), b.Dy()
	w, h := W, H
	if o >= 5 {
		w, h = H, W
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx, sy := mapOrientation(o, x, y, W, H)
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

// mapOrientation maps an output pixel to its source pixel for one EXIF
// orientation. W and H are the source dimensions.
func mapOrientation(o, x, y, W, H int) (int, int) {
	switch o {
	case 2:
		return W - 1 - x, y
	case 3:
		return W - 1 - x, H - 1 - y
	case 4:
		return x, H - 1 - y
	case 5:
		return y, x
	case 6:
		return y, H - 1 - x
	case 7:
		return W - 1 - y, H - 1 - x
	case 8:
		return W - 1 - y, x
	}
	return x, y
}
