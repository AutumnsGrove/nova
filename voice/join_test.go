package voice

import (
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
