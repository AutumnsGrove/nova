package voice

import (
	"strings"
	"testing"
)

func TestSpeak_A01_Validate(t *testing.T) {
	speed := func(f float64) *float64 { return &f }
	cases := []struct {
		name string
		req  SpeechRequest
		want bool // true means valid
	}{
		{"minimal", SpeechRequest{Model: "openai/gpt-4o-mini-tts", Voice: "alloy", Input: "hi"}, true},
		{"role name needs no voice yet", SpeechRequest{Model: "narrate", Input: "hi"}, true},
		{"empty model", SpeechRequest{Voice: "alloy", Input: "hi"}, false},
		{"empty input", SpeechRequest{Model: "openai/x", Voice: "alloy"}, false},
		{"blank input", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: "   "}, false},
		{"direct id without a voice", SpeechRequest{Model: "openai/x", Input: "hi"}, false},
		{"mp3", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: "hi", Format: FormatMP3}, true},
		{"pcm", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: "hi", Format: FormatPCM}, true},
		{"wav is not a speech format", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: "hi", Format: FormatWAV}, false},
		{"speed at the floor", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: "hi", Speed: speed(0.25)}, true},
		{"speed at the ceiling", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: "hi", Speed: speed(4)}, true},
		{"speed below the floor", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: "hi", Speed: speed(0.1)}, false},
		{"speed above the ceiling", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: "hi", Speed: speed(4.5)}, false},
		{"input at the cap", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: strings.Repeat("a", MaxSpeechRunes)}, true},
		{"input over the cap", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: strings.Repeat("a", MaxSpeechRunes+1)}, false},
		{"runes, not bytes, count", SpeechRequest{Model: "openai/x", Voice: "alloy", Input: strings.Repeat("é", MaxSpeechRunes)}, true},
	}
	for _, tc := range cases {
		err := tc.req.Validate()
		if tc.want && err != nil {
			t.Errorf("%s: Validate() = %v, want nil", tc.name, err)
		}
		if !tc.want && err == nil {
			t.Errorf("%s: Validate() = nil, want an error", tc.name)
		}
	}
}

func TestTranscribe_A16_Validate(t *testing.T) {
	temp := func(f float64) *float64 { return &f }
	audio := []byte{1, 2, 3}
	cases := []struct {
		name string
		req  TranscribeRequest
		want bool
	}{
		{"minimal", TranscribeRequest{Model: "openai/whisper-1", Audio: audio, Format: FormatMP3}, true},
		{"wav", TranscribeRequest{Model: "openai/whisper-1", Audio: audio, Format: FormatWAV}, true},
		{"every documented input format", TranscribeRequest{Model: "openai/whisper-1", Audio: audio, Format: FormatWebM}, true},
		{"empty model", TranscribeRequest{Audio: audio, Format: FormatMP3}, false},
		{"empty audio", TranscribeRequest{Model: "openai/whisper-1", Format: FormatMP3}, false},
		{"no format", TranscribeRequest{Model: "openai/whisper-1", Audio: audio}, false},
		{"pcm is not an input format", TranscribeRequest{Model: "openai/whisper-1", Audio: audio, Format: FormatPCM}, false},
		{"audio over the cap", TranscribeRequest{Model: "openai/whisper-1", Audio: make([]byte, MaxAudioBytes+1), Format: FormatMP3}, false},
		{"temperature at zero", TranscribeRequest{Model: "openai/whisper-1", Audio: audio, Format: FormatMP3, Temperature: temp(0)}, true},
		{"temperature at one", TranscribeRequest{Model: "openai/whisper-1", Audio: audio, Format: FormatMP3, Temperature: temp(1)}, true},
		{"temperature out of range", TranscribeRequest{Model: "openai/whisper-1", Audio: audio, Format: FormatMP3, Temperature: temp(1.5)}, false},
	}
	for _, tc := range cases {
		err := tc.req.Validate()
		if tc.want && err != nil {
			t.Errorf("%s: Validate() = %v, want nil", tc.name, err)
		}
		if !tc.want && err == nil {
			t.Errorf("%s: Validate() = nil, want an error", tc.name)
		}
	}
}

func TestTranscribe_A17_Timestamps(t *testing.T) {
	req := TranscribeRequest{
		Model: "openai/whisper-1", Audio: []byte{1}, Format: FormatMP3,
		Timestamps: []string{"word"}, Verbose: false,
	}
	if err := req.Validate(); err == nil {
		t.Fatal("timestamps without verbose = nil, want an error")
	}
	req.Verbose = true
	if err := req.Validate(); err != nil {
		t.Fatalf("timestamps with verbose = %v, want nil", err)
	}
	req.Timestamps = []string{"chapter"}
	if err := req.Validate(); err == nil {
		t.Fatal("unknown timestamp granularity = nil, want an error")
	}
}

func TestFormat_MIME(t *testing.T) {
	cases := map[Format]string{
		FormatMP3:  "audio/mpeg",
		FormatPCM:  "audio/pcm",
		FormatWAV:  "audio/wav",
		FormatFLAC: "audio/flac",
		FormatM4A:  "audio/mp4",
		FormatOGG:  "audio/ogg",
		FormatWebM: "audio/webm",
		FormatAAC:  "audio/aac",
		"":         "",
		"nonsense": "",
	}
	for format, want := range cases {
		if got := format.MIME(); got != want {
			t.Errorf("Format(%q).MIME() = %q, want %q", format, got, want)
		}
	}
}
