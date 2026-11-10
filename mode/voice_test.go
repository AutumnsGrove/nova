package mode

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/voice"
)

// fixedSpeaker runs fn for each Speak call.
type fixedSpeaker struct {
	fn func(context.Context, voice.SpeechRequest) (*voice.Clip, error)
}

func (f fixedSpeaker) Speak(ctx context.Context, req voice.SpeechRequest) (*voice.Clip, error) {
	return f.fn(ctx, req)
}

// fixedTranscriber runs fn for each Transcribe call.
type fixedTranscriber struct {
	fn func(context.Context, voice.TranscribeRequest) (*voice.Transcript, error)
}

func (f fixedTranscriber) Transcribe(ctx context.Context, req voice.TranscribeRequest) (*voice.Transcript, error) {
	return f.fn(ctx, req)
}

func validSpeakCommand() proto.Command {
	return proto.Command{
		Type:   proto.CmdSpeak,
		Speech: &voice.SpeechRequest{Model: "narrate", Input: "hi"},
	}
}

func validTranscribeCommand() proto.Command {
	return proto.Command{
		Type:          proto.CmdTranscribe,
		Transcription: &voice.TranscribeRequest{Model: "default", Audio: []byte{1, 2}, Format: voice.FormatMP3},
	}
}

// TestSpeak_A22_ProtocolCommand round-trips one synthesis through the server:
// the clip's bytes survive as base64 in the response frame.
func TestSpeak_A22_ProtocolCommand(t *testing.T) {
	s, _ := testServer(t)
	s.speaker = fixedSpeaker{fn: func(_ context.Context, req voice.SpeechRequest) (*voice.Clip, error) {
		return &voice.Clip{
			Bytes: []byte{0xff, 0xf3, 0x01}, Format: voice.FormatMP3, MIME: "audio/mpeg",
			Model: req.Model, Voice: "af_heart", GenerationID: "gen-1",
			Usage: provider.Usage{CostUSD: 0.01, CostSource: "provider"},
		}, nil
	}}
	resp := s.Handle(context.Background(), validSpeakCommand())
	if !resp.OK {
		t.Fatalf("speak = %+v", resp)
	}
	var clip voice.Clip
	if err := json.Unmarshal(resp.Data, &clip); err != nil {
		t.Fatalf("response is not a clip: %v", err)
	}
	if !bytes.Equal(clip.Bytes, []byte{0xff, 0xf3, 0x01}) {
		t.Errorf("bytes = %v, want the base64 round trip", clip.Bytes)
	}
	if clip.Format != voice.FormatMP3 || clip.MIME != "audio/mpeg" || clip.GenerationID != "gen-1" {
		t.Errorf("clip = %+v", clip)
	}
	if clip.Usage.CostUSD != 0.01 || clip.Usage.CostSource != "provider" {
		t.Errorf("usage = %+v", clip.Usage)
	}
}

// TestTranscribe_A23_ProtocolCommand round-trips one transcription.
func TestTranscribe_A23_ProtocolCommand(t *testing.T) {
	s, _ := testServer(t)
	s.transcriber = fixedTranscriber{fn: func(_ context.Context, req voice.TranscribeRequest) (*voice.Transcript, error) {
		return &voice.Transcript{
			Text: "hello", Model: req.Model, Duration: 1.5,
			Usage: provider.Usage{CostUSD: 0.001, CostSource: "provider"},
		}, nil
	}}
	resp := s.Handle(context.Background(), validTranscribeCommand())
	if !resp.OK {
		t.Fatalf("transcribe = %+v", resp)
	}
	var transcript voice.Transcript
	if err := json.Unmarshal(resp.Data, &transcript); err != nil {
		t.Fatalf("response is not a transcript: %v", err)
	}
	if transcript.Text != "hello" || transcript.Duration != 1.5 || transcript.Usage.CostUSD != 0.001 {
		t.Errorf("transcript = %+v", transcript)
	}
}

func TestSpeak_A22_MissingRequest(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSpeak})
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("speak without a request = %+v, want bad_request", resp)
	}
	resp = s.Handle(context.Background(), proto.Command{Type: proto.CmdTranscribe})
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("transcribe without a request = %+v, want bad_request", resp)
	}
}

// TestSpeak_A22_InvalidRequestNeverReachesTheSpeaker: local validation is the
// server's job before the call, so a missing voice costs nothing.
func TestSpeak_A22_InvalidRequestNeverReachesTheSpeaker(t *testing.T) {
	s, _ := testServer(t)
	called := false
	s.speaker = fixedSpeaker{fn: func(context.Context, voice.SpeechRequest) (*voice.Clip, error) {
		called = true
		return &voice.Clip{}, nil
	}}
	resp := s.Handle(context.Background(), proto.Command{
		Type:   proto.CmdSpeak,
		Speech: &voice.SpeechRequest{Model: "openai/gpt-4o-mini-tts", Input: "hi"},
	})
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("speak without a voice = %+v, want bad_request", resp)
	}
	if called {
		t.Error("the speaker was called for an invalid request")
	}
}

func TestSpeak_A22_ProviderError(t *testing.T) {
	s, _ := testServer(t)
	s.speaker = fixedSpeaker{fn: func(context.Context, voice.SpeechRequest) (*voice.Clip, error) {
		return nil, &provider.Error{Status: 502, Message: "upstream"}
	}}
	resp := s.Handle(context.Background(), validSpeakCommand())
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeProviderError {
		t.Fatalf("speak = %+v, want provider_error", resp)
	}
}

func TestTranscribe_A23_ProviderError(t *testing.T) {
	s, _ := testServer(t)
	s.transcriber = fixedTranscriber{fn: func(context.Context, voice.TranscribeRequest) (*voice.Transcript, error) {
		return nil, &provider.Error{Status: 400, Message: "bad audio"}
	}}
	resp := s.Handle(context.Background(), validTranscribeCommand())
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeProviderError {
		t.Fatalf("transcribe = %+v, want provider_error", resp)
	}
}

// TestSpeak_A22_NoSpeakerIsAnError keeps the seam honest: a server with no
// speaker fails the command instead of panicking.
func TestSpeak_A22_NoSpeakerIsAnError(t *testing.T) {
	s, _ := testServer(t)
	s.speaker = nil
	if _, err := s.speakCall(context.Background(), voice.SpeechRequest{Model: "a/b", Voice: "v", Input: "hi"}); err == nil {
		t.Fatal("speakCall with no speaker = nil, want an error")
	}
	if _, err := s.transcribeCall(context.Background(), voice.TranscribeRequest{Model: "a/b", Audio: []byte{1}, Format: voice.FormatMP3}); err == nil {
		t.Fatal("transcribeCall with no transcriber = nil, want an error")
	}
}
