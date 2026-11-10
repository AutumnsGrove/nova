package voice

import "encoding/binary"

// wavHeaderBytes is the size of a canonical RIFF/PCM header: 44 bytes, which is
// the three chunk headers plus the fmt body and the data size.
const wavHeaderBytes = 44

// WAV prepends a 44-byte RIFF/PCM header to a raw PCM clip, using the clip's own
// sample layout, so a caller can write a playable file without knowing the
// numbers. It returns nil for a non-PCM clip or an unknown layout, because a
// guessed header is a file that plays at the wrong speed.
func WAV(clip Clip) []byte {
	if clip.Format != FormatPCM || clip.SampleRate <= 0 || clip.Channels <= 0 || clip.BitsPerSample <= 0 {
		return nil
	}
	blockAlign := clip.Channels * clip.BitsPerSample / 8
	if blockAlign <= 0 {
		return nil
	}
	data := clip.Bytes
	out := make([]byte, wavHeaderBytes+len(data))
	le := binary.LittleEndian
	copy(out[0:4], "RIFF")
	le.PutUint32(out[4:8], uint32(36+len(data)))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	le.PutUint32(out[16:20], 16) // fmt chunk size
	le.PutUint16(out[20:22], 1)  // format 1: PCM
	le.PutUint16(out[22:24], uint16(clip.Channels))
	le.PutUint32(out[24:28], uint32(clip.SampleRate))
	le.PutUint32(out[28:32], uint32(clip.SampleRate*blockAlign))
	le.PutUint16(out[32:34], uint16(blockAlign))
	le.PutUint16(out[34:36], uint16(clip.BitsPerSample))
	copy(out[36:40], "data")
	le.PutUint32(out[40:44], uint32(len(data)))
	copy(out[wavHeaderBytes:], data)
	return out
}
