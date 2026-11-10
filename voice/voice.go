// Package voice defines the contract for the two audio modalities: text in,
// speech out, and speech in, text out, with the cost and provenance of the call.
//
// It is deliberately not a provider.Provider method. Speech has no messages, no
// streaming and no tools, and a transcription carries audio rather than a
// conversation, so the shape that fits is the one embed and decide use: a small
// contract, one wire adapter per dialect, and a role table over the shared roles
// resolver. There is no player, recorder or store here; the caller owns its
// audio stack, and a terminal is not an audio device.
package voice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/provider"
)

const (
	// MaxSpeechRunes caps one synthesis input before it leaves the process. It
	// bounds the request size; a model's own input limit may be far lower, which
	// is why Chunks exists and why the CLI chunks by default.
	MaxSpeechRunes = 32_000
	// MaxClipBytes bounds a synthesized clip.
	MaxClipBytes = 40 << 20
	// MaxAudioBytes bounds an audio input.
	MaxAudioBytes = 25 << 20
	// minSpeed and maxSpeed bound a playback multiplier: the widest range any
	// documented model accepts.
	minSpeed = 0.25
	maxSpeed = 4.0
)

// Kind is one direction of the audio contract. The value is the output modality
// the live roster filters on, so the roster and the catalog agree.
type Kind string

const (
	KindSpeech        Kind = "speech"
	KindTranscription Kind = "transcription"
)

// Valid reports whether k names a direction.
func (k Kind) Valid() bool { return k == KindSpeech || k == KindTranscription }

// Format is an audio container. A synthesis produces mp3 or pcm; a
// transcription accepts the documented input formats.
type Format string

const (
	FormatMP3  Format = "mp3"
	FormatPCM  Format = "pcm"
	FormatWAV  Format = "wav"
	FormatFLAC Format = "flac"
	FormatM4A  Format = "m4a"
	FormatOGG  Format = "ogg"
	FormatWebM Format = "webm"
	FormatAAC  Format = "aac"
)

// MIME returns the media type for a format, or "" for one this package does not
// know.
func (f Format) MIME() string {
	switch f {
	case FormatMP3:
		return "audio/mpeg"
	case FormatPCM:
		return "audio/pcm"
	case FormatWAV:
		return "audio/wav"
	case FormatFLAC:
		return "audio/flac"
	case FormatM4A:
		return "audio/mp4"
	case FormatOGG:
		return "audio/ogg"
	case FormatWebM:
		return "audio/webm"
	case FormatAAC:
		return "audio/aac"
	}
	return ""
}

// speechFormat reports whether f is a synthesis output. The empty format is
// allowed and means the adapter's default.
func (f Format) speechFormat() bool { return f == "" || f == FormatMP3 || f == FormatPCM }

// inputFormat reports whether f is an accepted transcription input. The empty
// format is refused: the bytes' container is a fact the caller has.
func (f Format) inputFormat() bool {
	switch f {
	case FormatWAV, FormatMP3, FormatFLAC, FormatM4A, FormatOGG, FormatWebM, FormatAAC:
		return true
	}
	return false
}

// Speaker synthesizes speech. Implementations must honor ctx cancellation and
// must return a non-nil error on failure.
type Speaker interface {
	Speak(ctx context.Context, req SpeechRequest) (*Clip, error)
}

// Transcriber turns audio into text. Implementations must honor ctx
// cancellation and must return a non-nil error on failure.
type Transcriber interface {
	Transcribe(ctx context.Context, req TranscribeRequest) (*Transcript, error)
}

// Catalog reads a live roster for one direction. voice/openrouter's Client
// implements it, so a caller can list models without naming a vendor.
type Catalog interface {
	List(ctx context.Context, kind Kind) ([]provider.Model, error)
}

// SpeechRequest is one synthesis call. Model is a catalog id or a configured
// role name; a role may supply the voice, format, speed and instructions, and
// an explicit request value wins.
type SpeechRequest struct {
	Model string `json:"model"`
	// Input is the text to speak. It is one string; multi-speaker turns are not
	// modelled.
	Input string `json:"input"`
	// Voice is a voice identifier the model accepts. It is required for a
	// direct model id and may come from a role otherwise.
	Voice string `json:"voice,omitempty"`
	// Format is "mp3" or "pcm"; empty means mp3. PCM from the provider is a
	// headerless stream, so a caller that wants a file should use WAV.
	Format Format `json:"format,omitempty"`
	// Speed is a playback multiplier. A pointer, so 1.0 is explicit.
	Speed *float64 `json:"speed,omitempty"`
	// Instructions are delivery notes such as tone or pacing, where the model
	// supports them.
	Instructions string `json:"instructions,omitempty"`
}

// Validate checks a request before it reaches the wire, so a malformed input is
// a local error instead of a 400 from the provider.
func (r SpeechRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return errors.New("voice: model is required")
	}
	if strings.TrimSpace(r.Input) == "" {
		return errors.New("voice: input is required")
	}
	if n := utf8.RuneCountInString(r.Input); n > MaxSpeechRunes {
		return fmt.Errorf("voice: input is %d runes, over the %d cap; split it with voice.Chunks", n, MaxSpeechRunes)
	}
	if !r.Format.speechFormat() {
		return fmt.Errorf("voice: format %q is not a speech format; use mp3 or pcm", r.Format)
	}
	if r.Speed != nil && (*r.Speed < minSpeed || *r.Speed > maxSpeed) {
		return fmt.Errorf("voice: speed %v is outside %v to %v", *r.Speed, minSpeed, maxSpeed)
	}
	// A name with a slash is a direct catalog id, so no role can supply a voice
	// for it. The shared resolver uses the same rule.
	if strings.Contains(r.Model, "/") && strings.TrimSpace(r.Voice) == "" {
		return fmt.Errorf("voice: model %q needs a voice; name one or use a role that sets it", r.Model)
	}
	return nil
}

// Clip is synthesized audio. Bytes are raw in Go and base64 on the wire.
type Clip struct {
	Bytes []byte `json:"audio"`
	// Format is the container the provider produced.
	Format Format `json:"format"`
	// MIME is the response's media type.
	MIME string `json:"mime"`
	// SampleRate, Channels and BitsPerSample describe a pcm clip's layout,
	// parsed from the response Content-Type. They are zero for mp3.
	SampleRate    int `json:"sample_rate,omitempty"`
	Channels      int `json:"channels,omitempty"`
	BitsPerSample int `json:"bits_per_sample,omitempty"`
	// Model is the configured id, not the provider's echo, so a stable role
	// does not look like a model change.
	Model string `json:"model"`
	// Voice is the voice that spoke, when the provider's request named one.
	Voice string `json:"voice"`
	// GenerationID is the provider's id for the call, when it reports one. A
	// caller can resolve it later for exact cost.
	GenerationID string `json:"generation_id,omitempty"`
	// Usage is the call's accounting. Speech reports no usage inline, so
	// CostUSD is set only when a generation lookup answered.
	Usage provider.Usage `json:"usage"`
}

// TranscribeRequest is one transcription call. Model is a catalog id or a
// configured role name.
type TranscribeRequest struct {
	Model string `json:"model"`
	// Audio is the raw bytes to transcribe.
	Audio []byte `json:"audio"`
	// Format is the container of Audio. It is required, because the wire needs
	// to know how to decode the bytes.
	Format Format `json:"format"`
	// Language is an ISO-639-1 code; empty auto-detects.
	Language string `json:"language,omitempty"`
	// Temperature is the sampling temperature, 0 to 1.
	Temperature *float64 `json:"temperature,omitempty"`
	// Timestamps asks for "segment" or "word" granularity. It needs Verbose.
	Timestamps []string `json:"timestamps,omitempty"`
	// Verbose asks for verbose_json, which carries duration, language and any
	// requested timestamps.
	Verbose bool `json:"verbose,omitempty"`
}

// Validate checks a transcription request before it reaches the wire.
func (r TranscribeRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return errors.New("voice: model is required")
	}
	if len(r.Audio) == 0 {
		return errors.New("voice: audio is required")
	}
	if len(r.Audio) > MaxAudioBytes {
		return fmt.Errorf("voice: audio is %d bytes, over the %d cap", len(r.Audio), MaxAudioBytes)
	}
	if !r.Format.inputFormat() {
		return fmt.Errorf("voice: format %q is not an input format", r.Format)
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 1) {
		return fmt.Errorf("voice: temperature %v is outside 0 to 1", *r.Temperature)
	}
	for _, granularity := range r.Timestamps {
		if granularity != "segment" && granularity != "word" {
			return fmt.Errorf("voice: timestamp granularity %q is not segment or word", granularity)
		}
	}
	if len(r.Timestamps) > 0 && !r.Verbose {
		return errors.New("voice: timestamps need verbose, because only verbose_json carries them")
	}
	return nil
}

// Transcript is one transcription call's result. Text is the wire's text with
// its padding trimmed; an empty text is a valid answer for silent audio, not an
// error.
type Transcript struct {
	Text string `json:"text"`
	// Language is what the provider reported, which may be a full name such as
	// "english" even though the request takes an ISO-639-1 code.
	Language string    `json:"language,omitempty"`
	Duration float64   `json:"duration,omitempty"`
	Segments []Segment `json:"segments,omitempty"`
	Words    []Word    `json:"words,omitempty"`
	Model    string    `json:"model"`
	// GenerationID is the provider's id for the call, when it reports one.
	GenerationID string         `json:"generation_id,omitempty"`
	Usage        provider.Usage `json:"usage"`
}

// Segment is one timestamped span of a verbose transcript.
type Segment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker string  `json:"speaker,omitempty"`
}

// Word is one timestamped word of a verbose transcript.
type Word struct {
	Word       string   `json:"word"`
	Start      float64  `json:"start"`
	End        float64  `json:"end"`
	Speaker    string   `json:"speaker,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
}
