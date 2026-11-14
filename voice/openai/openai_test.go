package openai_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/voice"
	"github.com/AutumnsGrove/nova/voice/openai"
)

const testKey = "sk-local-secret-value-123"

// newClient builds a client pointed at one scripted server with no key, which is
// the local-engine case.
func newClient(t *testing.T, turns ...testkit.VoiceServerTurn) (*openai.Client, *testkit.VoiceServer) {
	t.Helper()
	srv := testkit.NewVoiceServer(t, turns...)
	return openai.New(openai.Config{BaseURL: srv.URL}), srv
}

// newKeyedClient builds a keyed client, which is the hosted case.
func newKeyedClient(t *testing.T, turns ...testkit.VoiceServerTurn) (*openai.Client, *testkit.VoiceServer) {
	t.Helper()
	srv := testkit.NewVoiceServer(t, turns...)
	return openai.New(openai.Config{BaseURL: srv.URL, APIKey: testKey}), srv
}

// TestSpeak_L01_SpeechMapsToClip: a generation-less response still fills a Clip,
// and the MIME comes from the response rather than from the request.
func TestSpeak_L01_SpeechMapsToClip(t *testing.T) {
	audio := []byte{0xff, 0xf3, 0x84, 0xc4, 0, 0}
	client, srv := newClient(t, testkit.VoiceServerTurn{Audio: audio, ContentType: "audio/mpeg"})
	clip, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "kokoro", Voice: "af_heart", Input: "Hi.",
	})
	if err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if !bytes.Equal(clip.Bytes, audio) {
		t.Errorf("bytes = %v, want %v", clip.Bytes, audio)
	}
	if clip.Format != voice.FormatMP3 || clip.MIME != "audio/mpeg" {
		t.Errorf("format/mime = %q/%q, want mp3/audio/mpeg", clip.Format, clip.MIME)
	}
	if clip.Model != "kokoro" || clip.Voice != "af_heart" {
		t.Errorf("model/voice = %q/%q", clip.Model, clip.Voice)
	}
	if clip.GenerationID != "" {
		t.Errorf("generation id = %q, want none from a local server", clip.GenerationID)
	}
	if clip.Usage.CostUSD != 0 || clip.Usage.CostSource != "" {
		t.Errorf("usage = %+v, want no cost and no source: a free call is not an unknown one", clip.Usage)
	}
	if reqs := srv.Requests(); len(reqs) != 1 || reqs[0].Path != "/audio/speech" {
		t.Errorf("requests = %+v, want one POST /audio/speech", reqs)
	}
}

// TestSpeak_L01_PCMLayout: the sample layout comes from the response's
// Content-Type, so a local engine's 16 kHz stereo is not read as Kokoro's
// numbers.
func TestSpeak_L01_PCMLayout(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{
		Audio:       []byte{0, 1, 2, 3},
		ContentType: "audio/pcm;rate=16000;channels=2",
	})
	clip, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "piper", Voice: "en_US-lessac", Input: "Hi.", Format: voice.FormatPCM,
	})
	if err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if clip.Format != voice.FormatPCM {
		t.Fatalf("format = %q, want pcm", clip.Format)
	}
	if clip.SampleRate != 16000 || clip.Channels != 2 || clip.BitsPerSample != 16 {
		t.Errorf("layout = %d/%d/%d, want 16000/2/16", clip.SampleRate, clip.Channels, clip.BitsPerSample)
	}
	if wav := voice.WAV(*clip); len(wav) != len(clip.Bytes)+44 {
		t.Errorf("WAV length = %d, want the clip plus a header", len(wav))
	}
}

// TestSpeak_L02_LocalSendsNoAuthorization: a local engine needs no key, and a
// request that carries none must not invent a header.
func TestSpeak_L02_LocalSendsNoAuthorization(t *testing.T) {
	client, srv := newClient(t, testkit.VoiceServerTurn{Audio: []byte{0xff, 0xf3}, ContentType: "audio/mpeg"})
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "kokoro", Voice: "af_heart", Input: "Hi."}); err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	req := srv.Requests()[0]
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none for a keyless engine", got)
	}
}

// TestSpeak_L02_KeyIsSent: a keyed base URL sends the token as a bearer.
func TestSpeak_L02_KeyIsSent(t *testing.T) {
	client, srv := newKeyedClient(t, testkit.VoiceServerTurn{Audio: []byte{0xff, 0xf3}, ContentType: "audio/mpeg"})
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "gpt-4o-mini-tts", Voice: "alloy", Input: "Hi."}); err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	req := srv.Requests()[0]
	if got := req.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want the bearer token", got)
	}
}

// TestSpeak_L02_KeyFuncWins: the key is resolved per request, so a login takes
// effect mid-run.
func TestSpeak_L02_KeyFuncWins(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{0xff, 0xf3}, ContentType: "audio/mpeg"})
	client := openai.New(openai.Config{
		BaseURL: srv.URL,
		APIKey:  "sk-shadowed-value-123",
		APIKeyFunc: func(context.Context) (string, error) {
			return testKey, nil
		},
	})
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "kokoro", Voice: "af_heart", Input: "Hi."}); err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if got := srv.Requests()[0].Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want the resolved key", got)
	}
}

// TestTranscribe_L03_Multipart: the default shape is multipart with the file
// named clip.<format>, which is what every local server accepts.
func TestTranscribe_L03_Multipart(t *testing.T) {
	client, srv := newClient(t, testkit.VoiceServerTurn{Body: `{"text":"Hi.","language":"english"}`})
	temp := 0.2
	got, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "whisper-1", Audio: []byte("audio-bytes"), Format: voice.FormatMP3,
		Language: "en", Temperature: &temp, Verbose: true, Timestamps: []string{"word"},
	})
	if err != nil {
		t.Fatalf("Transcribe() = %v", err)
	}
	if got.Text != "Hi." || got.Language != "english" || got.Model != "whisper-1" {
		t.Errorf("transcript = %+v", got)
	}
	req := srv.Requests()[0]
	if req.Path != "/audio/transcriptions" {
		t.Errorf("path = %q", req.Path)
	}
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q, want multipart/form-data", req.Header.Get("Content-Type"))
	}
	form, err := multipart.NewReader(bytes.NewReader(req.Body), params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("reading form: %v", err)
	}
	file, ok := form.File["file"]
	if !ok || len(file) != 1 {
		t.Fatalf("file parts = %v, want one", form.File)
	}
	if file[0].Filename != "clip.mp3" {
		t.Errorf("filename = %q, want clip.mp3: the codec is inferred from it", file[0].Filename)
	}
	want := map[string]string{
		"model": "whisper-1", "language": "en", "temperature": "0.2",
		"response_format": "verbose_json", "timestamp_granularities[]": "word",
	}
	for field, value := range want {
		if got := form.Value[field]; len(got) != 1 || got[0] != value {
			t.Errorf("field %s = %v, want %q", field, got, value)
		}
	}
}

// TestTranscribe_L03_JSONMode: the base64 input_audio shape is the option for a
// server that only implements it.
func TestTranscribe_L03_JSONMode(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Body: `{"text":"Hi."}`})
	client := openai.New(openai.Config{BaseURL: srv.URL, TranscribeJSON: true})
	if _, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "whisper-1", Audio: []byte("audio-bytes"), Format: voice.FormatMP3,
	}); err != nil {
		t.Fatalf("Transcribe() = %v", err)
	}
	req := srv.Requests()[0]
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q, want application/json", ct)
	}
	var body struct {
		Model      string `json:"model"`
		InputAudio struct {
			Data   string `json:"data"`
			Format string `json:"format"`
		} `json:"input_audio"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("body = %s: %v", req.Body, err)
	}
	if body.Model != "whisper-1" || body.InputAudio.Format != "mp3" {
		t.Errorf("body = %+v", body)
	}
	if body.InputAudio.Data != base64.StdEncoding.EncodeToString([]byte("audio-bytes")) {
		t.Errorf("data = %q, want the base64 bytes", body.InputAudio.Data)
	}
}

// TestTranscribe_L04_NoUsageIsFreeNotUnknown: a local server usually reports no
// usage at all, and that is a free call rather than a failed cost lookup.
func TestTranscribe_L04_NoUsageIsFreeNotUnknown(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Body: `{"text":"Hi."}`})
	got, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "whisper-1", Audio: []byte("a"), Format: voice.FormatMP3,
	})
	if err != nil {
		t.Fatalf("Transcribe() = %v", err)
	}
	if got.Usage.CostUSD != 0 || got.Usage.CostSource != "" {
		t.Errorf("usage = %+v, want zero with no source", got.Usage)
	}
	if got.Usage.TotalTokens != 0 {
		t.Errorf("tokens = %d, want none", got.Usage.TotalTokens)
	}
}

// TestTranscribe_L04_ReportedUsageIsProviderCost: a server that does report
// usage is priced from it.
func TestTranscribe_L04_ReportedUsageIsProviderCost(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{
		Body: `{"text":"Hi.","duration":1.5,"usage":{"seconds":1.5,"input_tokens":7,"output_tokens":2,"cost":0.0001}}`,
	})
	got, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "whisper-1", Audio: []byte("a"), Format: voice.FormatMP3,
	})
	if err != nil {
		t.Fatalf("Transcribe() = %v", err)
	}
	if got.Usage.CostUSD != 0.0001 || got.Usage.CostSource != "provider" {
		t.Errorf("usage = %+v, want the reported cost", got.Usage)
	}
	if got.Usage.PromptTokens != 7 || got.Usage.CompletionTokens != 2 || got.Usage.TotalTokens != 9 {
		t.Errorf("tokens = %+v", got.Usage)
	}
	if got.Duration != 1.5 {
		t.Errorf("duration = %v, want 1.5", got.Duration)
	}
}

// TestTranscribe_L04_EmptyTextIsNotAnError: silence is a real answer.
func TestTranscribe_L04_EmptyTextIsNotAnError(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Body: `{"text":"  "}`})
	got, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "whisper-1", Audio: []byte("a"), Format: voice.FormatMP3,
	})
	if err != nil {
		t.Fatalf("Transcribe() = %v, want an empty transcript", err)
	}
	if got.Text != "" {
		t.Errorf("text = %q, want the padding trimmed to empty", got.Text)
	}
}

// TestTranscribe_L05_ErrorIsTyped: a local server's 400 is a typed error with
// the body, never a silent empty transcript.
func TestTranscribe_L05_ErrorIsTyped(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{
		Status: http.StatusBadRequest,
		Body:   `{"error":{"message":"unsupported format"}}`,
	})
	_, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "whisper-1", Audio: []byte("a"), Format: voice.FormatMP3,
	})
	var perr *provider.Error
	if !errors.As(err, &perr) {
		t.Fatalf("error = %v, want *provider.Error", err)
	}
	if perr.Status != http.StatusBadRequest || !strings.Contains(perr.Message, "unsupported format") {
		t.Errorf("error = %+v, want the status and the body", perr)
	}
	if perr.Retryable {
		t.Error("a 400 is not retryable")
	}
}

// TestSpeak_L05_ErrorScrubsTheKey: a gateway that echoes a rejected key must not
// put it in an error that reaches the screen or a log.
func TestSpeak_L05_ErrorScrubsTheKey(t *testing.T) {
	client, _ := newKeyedClient(t, testkit.VoiceServerTurn{
		Status: http.StatusUnauthorized,
		Body:   `{"error":{"message":"bad key ` + testKey + `"}}`,
	})
	_, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "kokoro", Voice: "af", Input: "Hi."})
	if err == nil {
		t.Fatal("Speak() = nil, want an error")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("error leaks the key: %v", err)
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Status != http.StatusUnauthorized {
		t.Errorf("error = %v, want a typed 401", err)
	}
	if strings.Contains(perr.Body, testKey) {
		t.Errorf("body leaks the key: %q", perr.Body)
	}
}

// TestSpeak_L05_EmptyAudioIsAnError: a 200 with no bytes is not a clip.
func TestSpeak_L05_EmptyAudioIsAnError(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Audio: []byte{}, ContentType: "audio/mpeg"})
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "kokoro", Voice: "af", Input: "Hi."}); err == nil {
		t.Fatal("Speak() = nil, want an error for an empty body")
	}
}

// TestSpeak_L08_CancelIsACancel: a cancelled caller gets the context's error, so
// a retry loop above cannot mistake it for an engine failure.
func TestSpeak_L08_CancelIsACancel(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Hang: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Speak(ctx, voice.SpeechRequest{Model: "kokoro", Voice: "af", Input: "Hi."})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// TestSpeak_L01_ValidateLocally: a malformed request costs no request.
func TestSpeak_L01_ValidateLocally(t *testing.T) {
	client, srv := newClient(t)
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "kokoro", Input: ""}); err == nil {
		t.Fatal("Speak() = nil, want a validation error for empty input")
	}
	if reqs := srv.Requests(); len(reqs) != 0 {
		t.Errorf("requests = %d, want none", len(reqs))
	}
}

// TestVoice_L08_NoLeaks: a speak and a transcribe leave no goroutine behind,
// including a cancelled one, because a long-lived `nova serve` makes one per
// request.
func TestVoice_L08_NoLeaks(t *testing.T) {
	testkit.NoLeaks(t)
	client, _ := newClient(t,
		testkit.VoiceServerTurn{Audio: []byte{0xff, 0xf3}, ContentType: "audio/mpeg"},
		testkit.VoiceServerTurn{Body: `{"text":"hi"}`},
		testkit.VoiceServerTurn{Hang: true},
	)
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "kokoro", Voice: "af", Input: "Hi."}); err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if _, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "whisper-1", Audio: []byte("a"), Format: voice.FormatMP3,
	}); err != nil {
		t.Fatalf("Transcribe() = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Speak(ctx, voice.SpeechRequest{Model: "kokoro", Voice: "af", Input: "Hi."}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Speak() = %v, want context.Canceled", err)
	}
}
