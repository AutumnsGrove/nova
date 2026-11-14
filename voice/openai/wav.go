package openai

import (
	"encoding/binary"
	"errors"
)

// errBadWAV reports a body labelled audio/wav that is not one.
var errBadWAV = errors.New("voice: response is labelled wav but is not a wav file")

// wavToPCM unpacks a RIFF/WAVE body into its samples and sample layout.
//
// A WAV file is PCM with a header, and some local engines answer one whatever
// the request asked for (a Piper server does). Repackaging it as the pcm clip it
// is keeps everything downstream correct: `voice.Join` concatenates samples
// rather than whole files, and `voice.WAV` writes one header over the result
// instead of leaving a header mid-stream. Only plain PCM (format 1) is accepted;
// a compressed WAV is refused rather than misread.
func wavToPCM(data []byte) (pcm []byte, rate, channels, bits int, ok bool) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, 0, 0, false
	}
	var haveFmt, haveData bool
	for offset := 12; offset+8 <= len(data); {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if size < 0 || body+size > len(data) {
			return nil, 0, 0, 0, false
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, 0, 0, 0, false
			}
			if format := binary.LittleEndian.Uint16(data[body : body+2]); format != 1 {
				return nil, 0, 0, 0, false
			}
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			rate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			bits = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			haveFmt = true
		case "data":
			pcm = data[body : body+size]
			haveData = true
		}
		// Chunks are word-aligned: an odd size is followed by one pad byte.
		offset = body + size
		if size%2 == 1 {
			offset++
		}
	}
	if !haveFmt || !haveData || channels <= 0 || rate <= 0 || bits <= 0 {
		return nil, 0, 0, 0, false
	}
	return pcm, rate, channels, bits, true
}
