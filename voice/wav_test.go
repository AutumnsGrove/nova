package voice

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWAV_A21(t *testing.T) {
	samples := []byte{0, 0, 1, 0, 2, 0, 3, 0}
	clip := pcmClip(24000, 1, 16, samples...)
	got := WAV(clip)
	if got == nil {
		t.Fatal("WAV() = nil, want a header")
	}
	if len(got) != 44+len(samples) {
		t.Fatalf("WAV() is %d bytes, want %d", len(got), 44+len(samples))
	}
	if string(got[0:4]) != "RIFF" || string(got[8:12]) != "WAVE" || string(got[12:16]) != "fmt " || string(got[36:40]) != "data" {
		t.Fatalf("WAV() chunks = %q %q %q %q", got[0:4], got[8:12], got[12:16], got[36:40])
	}
	le := binary.LittleEndian
	checks := []struct {
		name string
		at   int
		got  uint32
		want uint32
	}{
		{"riff size", 4, le.Uint32(got[4:8]), uint32(36 + len(samples))},
		{"fmt size", 16, le.Uint32(got[16:20]), 16},
		{"audio format", 20, uint32(le.Uint16(got[20:22])), 1},
		{"channels", 22, uint32(le.Uint16(got[22:24])), 1},
		{"sample rate", 24, le.Uint32(got[24:28]), 24000},
		{"byte rate", 28, le.Uint32(got[28:32]), 24000 * 2},
		{"block align", 32, uint32(le.Uint16(got[32:34])), 2},
		{"bits", 34, uint32(le.Uint16(got[34:36])), 16},
		{"data size", 40, le.Uint32(got[40:44]), uint32(len(samples))},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("WAV() %s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if !bytes.Equal(got[44:], samples) {
		t.Errorf("WAV() samples = %v, want %v", got[44:], samples)
	}
}

func TestWAV_A21_Stereo(t *testing.T) {
	clip := pcmClip(16000, 2, 16, 1, 2, 3, 4)
	got := WAV(clip)
	if got == nil {
		t.Fatal("WAV() = nil")
	}
	if block := binary.LittleEndian.Uint16(got[32:34]); block != 4 {
		t.Errorf("block align = %d, want 4", block)
	}
	if rate := binary.LittleEndian.Uint32(got[28:32]); rate != 16000*4 {
		t.Errorf("byte rate = %d, want %d", rate, 16000*4)
	}
}

func TestWAV_A21_Refuses(t *testing.T) {
	cases := []struct {
		name string
		clip Clip
	}{
		{"mp3", Clip{Bytes: []byte{1}, Format: FormatMP3, SampleRate: 24000, Channels: 1, BitsPerSample: 16}},
		{"no sample rate", Clip{Bytes: []byte{1}, Format: FormatPCM, Channels: 1, BitsPerSample: 16}},
		{"no channels", Clip{Bytes: []byte{1}, Format: FormatPCM, SampleRate: 24000, BitsPerSample: 16}},
		{"no bit depth", Clip{Bytes: []byte{1}, Format: FormatPCM, SampleRate: 24000, Channels: 1}},
	}
	for _, tc := range cases {
		if got := WAV(tc.clip); got != nil {
			t.Errorf("%s: WAV() = %d bytes, want nil", tc.name, len(got))
		}
	}
}
