package voice

import (
	"bytes"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
)

func pcmClip(rate, channels, bits int, samples ...byte) Clip {
	return Clip{
		Bytes: samples, Format: FormatPCM, MIME: "audio/pcm",
		SampleRate: rate, Channels: channels, BitsPerSample: bits,
		Model: "hexgrad/kokoro-82m", Voice: "af_heart",
	}
}

func TestJoin_A22(t *testing.T) {
	a := Clip{Bytes: []byte{1, 2}, Format: FormatMP3, MIME: "audio/mpeg", Model: "m", Voice: "v", GenerationID: "gen-1", Usage: provider.Usage{CostUSD: 0.01, CostSource: "provider"}}
	b := Clip{Bytes: []byte{3, 4}, Format: FormatMP3, MIME: "audio/mpeg", Model: "m", Voice: "v", GenerationID: "gen-2", Usage: provider.Usage{CostUSD: 0.02, CostSource: "provider"}}
	got, err := Join(a, b)
	if err != nil {
		t.Fatalf("Join() = %v", err)
	}
	if string(got.Bytes) != string([]byte{1, 2, 3, 4}) {
		t.Errorf("Join() bytes = %v, want [1 2 3 4]", got.Bytes)
	}
	if got.Format != FormatMP3 || got.MIME != "audio/mpeg" || got.Model != "m" || got.Voice != "v" {
		t.Errorf("Join() lost the clip metadata: %+v", got)
	}
	if got.GenerationID != "" {
		t.Errorf("Join() kept one generation id %q; a joined clip has many", got.GenerationID)
	}
	if got.Usage.CostUSD != 0.03 || got.Usage.CostSource != "provider" {
		t.Errorf("Join() usage = %+v, want cost 0.03 from the provider", got.Usage)
	}
}

func TestJoin_A22_PCMKeepsLayout(t *testing.T) {
	a := pcmClip(24000, 1, 16, 1, 2)
	b := pcmClip(24000, 1, 16, 3, 4)
	got, err := Join(a, b)
	if err != nil {
		t.Fatalf("Join() = %v", err)
	}
	if got.SampleRate != 24000 || got.Channels != 1 || got.BitsPerSample != 16 {
		t.Errorf("Join() layout = %d/%d/%d", got.SampleRate, got.Channels, got.BitsPerSample)
	}
	if string(got.Bytes) != string([]byte{1, 2, 3, 4}) {
		t.Errorf("Join() bytes = %v", got.Bytes)
	}
}

func TestJoin_A22_Refuses(t *testing.T) {
	mp3 := Clip{Bytes: []byte{1}, Format: FormatMP3}
	pcm := pcmClip(24000, 1, 16, 1)
	otherRate := pcmClip(16000, 1, 16, 2)
	otherChannels := pcmClip(24000, 2, 16, 3)
	otherBits := pcmClip(24000, 1, 8, 4)
	cases := []struct {
		name  string
		clips []Clip
	}{
		{"no clips", nil},
		{"mixed format", []Clip{mp3, pcm}},
		{"mixed sample rate", []Clip{pcm, otherRate}},
		{"mixed channels", []Clip{pcm, otherChannels}},
		{"mixed bit depth", []Clip{pcm, otherBits}},
	}
	for _, tc := range cases {
		if _, err := Join(tc.clips...); err == nil {
			t.Errorf("%s: Join() = nil error, want a refusal", tc.name)
		}
	}
}

func TestJoin_A22_DoesNotAlias(t *testing.T) {
	a := Clip{Bytes: []byte{1}, Format: FormatMP3}
	got, err := Join(a)
	if err != nil {
		t.Fatalf("Join() = %v", err)
	}
	got.Bytes[0] = 9
	if a.Bytes[0] != 1 {
		t.Error("Join() returned a clip that shares its buffer with the input")
	}
}

// id3Tag builds a minimal ID3v2 tag of the requested payload size.
func id3Tag(payload int) []byte {
	tag := make([]byte, 10+payload)
	copy(tag, "ID3")
	tag[3] = 4 // version 2.4
	tag[6] = byte(payload >> 21 & 0x7f)
	tag[7] = byte(payload >> 14 & 0x7f)
	tag[8] = byte(payload >> 7 & 0x7f)
	tag[9] = byte(payload & 0x7f)
	return tag
}

// TestJoin_A22_DropsATagMidStream: Voxtral tags every clip, so a join must keep
// one tag at the front and none in the middle.
func TestJoin_A22_DropsATagMidStream(t *testing.T) {
	first := Clip{Format: FormatMP3, Bytes: append(id3Tag(4), 0xff, 0xf3)}
	second := Clip{Format: FormatMP3, Bytes: append(id3Tag(20), 0xff, 0xf3, 0x01)}
	got, err := Join(first, second)
	if err != nil {
		t.Fatalf("Join() = %v", err)
	}
	if !bytes.Equal(got.Bytes[:14], id3Tag(4)) {
		t.Errorf("the first clip's tag was dropped: %x", got.Bytes[:14])
	}
	rest := got.Bytes[14:]
	if bytes.Contains(rest, []byte("ID3")) {
		t.Errorf("a tag survived mid-stream: %x", got.Bytes)
	}
	if !bytes.Equal(rest, []byte{0xff, 0xf3, 0xff, 0xf3, 0x01}) {
		t.Errorf("frames = %x", rest)
	}
}

func TestTrimID3_A22(t *testing.T) {
	frames := []byte{0xff, 0xf3, 0x01}
	cases := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"no tag", frames, frames},
		{"tag", append(id3Tag(10), frames...), frames},
		{"truncated tag", append(id3Tag(100), frames...)[:20], append(id3Tag(100), frames...)[:20]},
		{"shorter than a header", []byte("ID3"), []byte("ID3")},
	}
	for _, tc := range cases {
		if got := trimID3(tc.in); !bytes.Equal(got, tc.want) {
			t.Errorf("%s: trimID3() = %x, want %x", tc.name, got, tc.want)
		}
	}
	// A footer flag adds ten bytes to the tag, before the frames.
	withFooter := append(append(id3Tag(4), make([]byte, 10)...), frames...)
	withFooter[5] = 0x10
	if got := trimID3(withFooter); !bytes.Equal(got, frames) {
		t.Errorf("footer: trimID3() = %x, want the frames", got)
	}
}
