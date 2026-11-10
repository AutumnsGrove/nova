package mode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
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

// TestVoice_A26_ToolSeamWiresTheRole drives the whole path the way a real front
// door does: config role, the built clients, the run's agent.Config, the tool
// seam, and the usage hook. It exists because the first version of this wiring
// built the clients but never passed them into the loop, and a test that
// constructs agent.Config itself cannot see that.
func TestVoice_A26_ToolSeamWiresTheRole(t *testing.T) {
	t.Setenv(auth.EnvAPIKey, "test-key")
	voiceSrv := testkit.NewVoiceServer(t,
		testkit.VoiceServerTurn{Audio: []byte{1, 2}, ContentType: "audio/mpeg"},
		testkit.VoiceServerTurn{Body: `{"text":"hello"}`},
	)
	providerSrv := testkit.NewProvider(testkit.RealClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "narrate", Args: `{}`}}},
		testkit.Turn{Text: "done"},
	)
	var gotClip *voice.Clip
	var gotTranscript *voice.Transcript
	narrate := tool.New(tool.Info{
		Name:        "narrate",
		Description: "speak and transcribe",
		Parameters:  map[string]any{"type": "object"},
		ReadOnly:    true,
	}, func(ctx context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		if tctx.Speak == nil || tctx.Transcribe == nil {
			return tool.Errorf("no voice seam"), nil
		}
		clip, err := tctx.Speak(ctx, voice.SpeechRequest{Model: "narrate", Input: "hi"})
		if err != nil {
			return tool.Errorf("speak: %v", err), nil
		}
		gotClip = clip
		tctx.AddUsage(clip.Usage)
		transcript, err := tctx.Transcribe(ctx, voice.TranscribeRequest{Model: "default", Audio: []byte{1}, Format: voice.FormatMP3})
		if err != nil {
			return tool.Errorf("transcribe: %v", err), nil
		}
		gotTranscript = transcript
		tctx.AddUsage(transcript.Usage)
		return tool.Text("%s", transcript.Text), nil
	})

	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	cfg.Provider.OpenRouter.BaseURL = voiceSrv.URL
	lookup := false
	cfg.Voice.CostLookup = &lookup
	cfg.Voice.Speech.Roles = map[string]config.SpeechRoleConfig{
		"narrate": {Model: "hexgrad/kokoro-82m", Voice: "af_heart", Format: "mp3"},
	}
	cfg.Voice.Transcribe.Roles = map[string]config.TranscribeRoleConfig{
		"default": {Model: "openai/whisper-1"},
	}
	s, err := New(Options{
		Config:     &cfg,
		CWD:        t.TempDir(),
		Home:       t.TempDir(),
		Provider:   providerSrv,
		Session:    filepath.Join(t.TempDir(), "session.jsonl"),
		Version:    "test",
		Tools:      []string{"narrate"},
		ExtraTools: []tool.Tool{narrate},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if code := RunPrint(context.Background(), s, "go", io.Discard, io.Discard); code != 0 {
		t.Fatalf("RunPrint exit = %d", code)
	}
	if gotClip == nil || !bytes.Equal(gotClip.Bytes, []byte{1, 2}) || gotClip.Voice != "af_heart" {
		t.Fatalf("tool got clip %+v; requests: %+v", gotClip, voiceSrv.Requests())
	}
	if gotTranscript == nil || gotTranscript.Text != "hello" {
		t.Fatalf("tool got transcript %+v", gotTranscript)
	}
	reqs := voiceSrv.Requests()
	if len(reqs) != 2 || reqs[0].Path != "/audio/speech" || reqs[1].Path != "/audio/transcriptions" {
		t.Fatalf("requests = %+v", reqs)
	}
}

// TestVoice_Reload_AppliesNewRoles: /reload must rebuild the speaker and
// transcriber, or fresh voice.* settings sit in s.cfg while calls keep using the
// old role table.
func TestVoice_Reload_AppliesNewRoles(t *testing.T) {
	t.Setenv(auth.EnvAPIKey, "test-key")
	srv := testkit.NewVoiceServer(t,
		testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"},
		testkit.VoiceServerTurn{Audio: []byte{2}, ContentType: "audio/mpeg"},
	)
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	cfg.Provider.OpenRouter.BaseURL = srv.URL
	lookup := false
	cfg.Voice.CostLookup = &lookup
	cfg.Voice.Speech.Roles = map[string]config.SpeechRoleConfig{
		"narrate": {Model: "vendor/one", Voice: "v1"},
	}
	fresh := cfg
	s, err := New(Options{
		Config:   &cfg,
		Reload:   func() (*config.Config, []string, error) { return &fresh, nil, nil },
		CWD:      t.TempDir(),
		Home:     t.TempDir(),
		Provider: testkit.NewProvider(testkit.RealClock()),
		Session:  filepath.Join(t.TempDir(), "session.jsonl"),
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	speak := func() string {
		t.Helper()
		if _, err := s.speakCall(context.Background(), voice.SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
			t.Fatalf("speakCall: %v", err)
		}
		var body struct {
			Model string `json:"model"`
		}
		reqs := srv.Requests()
		if err := json.Unmarshal(reqs[len(reqs)-1].Body, &body); err != nil {
			t.Fatalf("request body: %v", err)
		}
		return body.Model
	}

	if got := speak(); got != "vendor/one" {
		t.Fatalf("before reload: model = %q", got)
	}
	fresh.Voice.Speech.Roles = map[string]config.SpeechRoleConfig{
		"narrate": {Model: "vendor/two", Voice: "v2"},
	}
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdReload}); !resp.OK {
		t.Fatalf("reload = %+v", resp)
	}
	if got := speak(); got != "vendor/two" {
		t.Fatalf("after reload: model = %q, want the new role table", got)
	}
}
