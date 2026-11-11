package openrouter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/voice"
	"github.com/AutumnsGrove/nova/voice/openrouter"
)

const testKey = "sk-secret-value-123"

// newClient builds a client pointed at one scripted server.
func newClient(t *testing.T, turns ...testkit.VoiceServerTurn) (*openrouter.Client, *testkit.VoiceServer) {
	t.Helper()
	srv := testkit.NewVoiceServer(t, turns...)
	return openrouter.New(openrouter.Config{BaseURL: srv.URL, APIKey: testKey}), srv
}

// newCostClient builds a client with the generation lookup turned on.
func newCostClient(t *testing.T, turns ...testkit.VoiceServerTurn) (*openrouter.Client, *testkit.VoiceServer) {
	t.Helper()
	srv := testkit.NewVoiceServer(t, turns...)
	return openrouter.New(openrouter.Config{BaseURL: srv.URL, APIKey: testKey, CostLookup: true}), srv
}

func TestSpeak_A02_MP3(t *testing.T) {
	audio := []byte{0xff, 0xf3, 0x84, 0xc4, 0, 0}
	client, srv := newClient(t, testkit.VoiceServerTurn{
		Audio: audio, ContentType: "audio/mpeg",
		Headers: map[string]string{"X-Generation-Id": "gen-1"},
	})
	clip, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.",
	})
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if !bytes.Equal(clip.Bytes, audio) {
		t.Errorf("bytes = %v, want %v", clip.Bytes, audio)
	}
	if clip.Format != voice.FormatMP3 || clip.MIME != "audio/mpeg" {
		t.Errorf("format/mime = %q/%q", clip.Format, clip.MIME)
	}
	if clip.Model != "hexgrad/kokoro-82m" || clip.Voice != "af_heart" {
		t.Errorf("model/voice = %q/%q", clip.Model, clip.Voice)
	}
	if clip.GenerationID != "gen-1" {
		t.Errorf("generation id = %q, want gen-1", clip.GenerationID)
	}
	if clip.SampleRate != 0 || clip.Channels != 0 {
		t.Errorf("an mp3 clip has a pcm layout: %+v", clip)
	}
	assertSpeechRequest(t, srv, voice.SpeechRequest{Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.", Format: voice.FormatMP3})
}

// assertSpeechRequest checks the wire body and headers of the first request.
func assertSpeechRequest(t *testing.T, srv *testkit.VoiceServer, want voice.SpeechRequest) {
	t.Helper()
	reqs := srv.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request recorded")
	}
	req := reqs[0]
	if req.Method != "POST" || req.Path != "/audio/speech" {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q", got)
	}
	if got := req.Header.Get("HTTP-Referer"); got == "" {
		t.Error("HTTP-Referer is missing")
	}
	if got := req.Header.Get("X-Title"); got == "" {
		t.Error("X-Title is missing")
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	var body struct {
		Model          string   `json:"model"`
		Input          string   `json:"input"`
		Voice          string   `json:"voice"`
		ResponseFormat string   `json:"response_format"`
		Speed          *float64 `json:"speed"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if body.Model != want.Model || body.Input != want.Input || body.Voice != want.Voice || body.ResponseFormat != string(want.Format) {
		t.Errorf("request body = %+v", body)
	}
}

func TestSpeak_A03_PCMLayout(t *testing.T) {
	samples := []byte{0, 0, 1, 0, 2, 0}
	client, _ := newClient(t, testkit.VoiceServerTurn{
		Audio: samples, ContentType: "audio/pcm;rate=24000;channels=1",
	})
	clip, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.", Format: voice.FormatPCM,
	})
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if clip.Format != voice.FormatPCM || clip.MIME != "audio/pcm" {
		t.Errorf("format/mime = %q/%q", clip.Format, clip.MIME)
	}
	if clip.SampleRate != 24000 || clip.Channels != 1 || clip.BitsPerSample != 16 {
		t.Errorf("layout = %d Hz/%d ch/%d bit", clip.SampleRate, clip.Channels, clip.BitsPerSample)
	}
	wav := voice.WAV(*clip)
	if wav == nil {
		t.Fatal("WAV() = nil for a pcm clip")
	}
	if len(wav) != 44+len(samples) {
		t.Errorf("WAV length = %d, want %d", len(wav), 44+len(samples))
	}
}

func TestSpeak_A04_PCMDefaults(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Audio: []byte{1, 2}, ContentType: "audio/pcm"})
	clip, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.", Format: voice.FormatPCM,
	})
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if clip.SampleRate != 24000 || clip.Channels != 1 || clip.BitsPerSample != 16 {
		t.Errorf("default layout = %d Hz/%d ch/%d bit", clip.SampleRate, clip.Channels, clip.BitsPerSample)
	}
}

func TestSpeak_A05_ProviderError(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{
		Status: 500,
		Body:   `{"error":{"message":"boom: ` + testKey + `"}}`,
	})
	_, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.",
	})
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %T %v, want *provider.Error", err, err)
	}
	if providerErr.Status != 500 || !providerErr.Retryable {
		t.Errorf("status/retryable = %d/%v", providerErr.Status, providerErr.Retryable)
	}
	if strings.Contains(providerErr.Message, testKey) || strings.Contains(providerErr.Body, testKey) {
		t.Error("the API key leaked into the error")
	}
}

func TestSpeak_A06_NoAudio(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Audio: []byte{}, ContentType: "audio/mpeg"})
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.",
	}); err == nil {
		t.Fatal("Speak() = nil, want an error for an empty body")
	}
}

func TestSpeak_A10_CostLookup(t *testing.T) {
	client, srv := newCostClient(t,
		testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg", Headers: map[string]string{"X-Generation-Id": "gen-1"}},
		testkit.VoiceServerTurn{Body: `{"data":{"api_type":"tts","total_cost":0.000123}}`},
	)
	clip, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.",
	})
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if clip.Usage.CostUSD != 0.000123 || clip.Usage.CostSource != "provider" {
		t.Errorf("usage = %+v, want the looked-up cost", clip.Usage)
	}
	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want the synthesis and the lookup", len(reqs))
	}
	if reqs[1].Path != "/generation" || reqs[1].Query != "id=gen-1" {
		t.Errorf("lookup request = %s?%s, want /generation?id=gen-1", reqs[1].Path, reqs[1].Query)
	}
	if got := reqs[1].Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("lookup Authorization = %q", got)
	}
}

func TestSpeak_A10_FailedLookupIsNotAFailedSynthesis(t *testing.T) {
	client, _ := newCostClient(t,
		testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg", Headers: map[string]string{"X-Generation-Id": "gen-1"}},
		testkit.VoiceServerTurn{Status: 500, Body: `{"error":{"message":"nope"}}`},
	)
	clip, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.",
	})
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if len(clip.Bytes) != 1 {
		t.Errorf("clip bytes = %v", clip.Bytes)
	}
	if clip.Usage.CostUSD != 0 || clip.Usage.CostSource != "" {
		t.Errorf("usage = %+v, want an unknown cost", clip.Usage)
	}
}

func TestSpeak_A10_LookupOffMakesOneRequest(t *testing.T) {
	client, srv := newClient(t, testkit.VoiceServerTurn{
		Audio: []byte{1}, ContentType: "audio/mpeg", Headers: map[string]string{"X-Generation-Id": "gen-1"},
	})
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.",
	}); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if got := len(srv.Requests()); got != 1 {
		t.Errorf("requests = %d, want 1 with the lookup off", got)
	}
}

func TestSpeak_A11_Cancelled(t *testing.T) {
	client, _ := newClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Speak(ctx, voice.SpeechRequest{Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi."})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestSpeak_A16_InvalidRequestNeverReachesTheServer(t *testing.T) {
	client, srv := newClient(t)
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "hexgrad/kokoro-82m", Input: "Hi."}); err == nil {
		t.Fatal("Speak() = nil, want a local validation error")
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

// speechBody decodes a recorded request into its top-level keys, so a test can
// ask whether a field was serialised at all rather than only what it held. The
// provider object is a map, and omitempty on a map is easy to get wrong, so
// both halves of A31 read the raw JSON.
func speechBody(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("request body %q: %v", body, err)
	}
	return top
}

// TestSpeak_A31_ProviderObjectIsVerbatim: a provider object is the provider's
// business, so it reaches the wire exactly as the caller wrote it.
func TestSpeak_A31_ProviderObjectIsVerbatim(t *testing.T) {
	client, srv := newClient(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	_, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.",
		Provider: map[string]any{
			"only":        []string{"Together"},
			"data_policy": "deny",
		},
	})
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	top := speechBody(t, reqs[0].Body)
	raw, ok := top["provider"]
	if !ok {
		t.Fatalf("body = %s, want a provider key", reqs[0].Body)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("provider = %s: %v", raw, err)
	}
	if only, ok := got["only"].([]any); !ok || len(only) != 1 || only[0] != "Together" {
		t.Errorf("provider.only = %v, want [Together]", got["only"])
	}
	if got["data_policy"] != "deny" {
		t.Errorf("provider.data_policy = %v, want deny: an unknown key is not dropped", got["data_policy"])
	}
}

// TestSpeak_A31_NoProviderOmitsTheKey: a request without a pin must not send an
// empty object, because an empty provider block changes routing on its own.
func TestSpeak_A31_NoProviderOmitsTheKey(t *testing.T) {
	client, srv := newClient(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.",
	}); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if top := speechBody(t, reqs[0].Body); top["provider"] != nil {
		t.Errorf("body = %s, want no provider key", reqs[0].Body)
	}
}

// TestSpeak_A34_NonsenseProviderReachesTheServer: no local validation. The
// provider object is the provider's business; a typo is its 400, which maps to
// a provider error like any other status.
func TestSpeak_A34_NonsenseProviderReachesTheServer(t *testing.T) {
	client, srv := newClient(t, testkit.VoiceServerTurn{
		Status: 400,
		Body:   `{"error":{"message":"unknown field lonly"}}`,
	})
	_, err := client.Speak(context.Background(), voice.SpeechRequest{
		Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi.",
		Provider: map[string]any{"lonly": []string{"Together"}},
	})
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1: the typo must reach the server", len(reqs))
	}
	if top := speechBody(t, reqs[0].Body); top["provider"] == nil {
		t.Errorf("body = %s, want the nonsense provider object to pass through", reqs[0].Body)
	}
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %T %v, want *provider.Error", err, err)
	}
	if providerErr.Status != 400 {
		t.Errorf("status = %d, want 400", providerErr.Status)
	}
}

func TestTranscribe_A12_Multipart(t *testing.T) {
	audio := []byte{1, 2, 3, 4}
	client, srv := newClient(t, testkit.VoiceServerTurn{
		Body: `{"text":"Hi."}`, Headers: map[string]string{"X-Generation-Id": "gen-2"},
	})
	temp := 0.5
	got, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "openai/whisper-1", Audio: audio, Format: voice.FormatMP3,
		Language: "en", Temperature: &temp, Verbose: true, Timestamps: []string{"word"},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.GenerationID != "gen-2" {
		t.Errorf("generation id = %q", got.GenerationID)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/audio/transcriptions" {
		t.Fatalf("requests = %+v", reqs)
	}
	mediaType, params, err := mime.ParseMediaType(reqs[0].Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("Content-Type = %q", reqs[0].Header.Get("Content-Type"))
	}
	reader := multipart.NewReader(bytes.NewReader(reqs[0].Body), params["boundary"])
	fields := map[string]string{}
	fileBytes := []byte(nil)
	fileName := ""
	fileType := ""
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		body := new(bytes.Buffer)
		if _, err := body.ReadFrom(part); err != nil {
			t.Fatalf("reading part: %v", err)
		}
		if part.FormName() == "file" {
			fileName, fileType, fileBytes = part.FileName(), part.Header.Get("Content-Type"), body.Bytes()
			continue
		}
		fields[part.FormName()] = body.String()
	}
	if fileName != "clip.mp3" {
		t.Errorf("file name = %q, want clip.mp3 (the provider infers the codec from it)", fileName)
	}
	if fileType != "audio/mpeg" {
		t.Errorf("file part Content-Type = %q", fileType)
	}
	if !bytes.Equal(fileBytes, audio) {
		t.Errorf("file bytes = %v, want %v", fileBytes, audio)
	}
	want := map[string]string{
		"model": "openai/whisper-1", "language": "en", "temperature": "0.5",
		"response_format": "verbose_json", "timestamp_granularities[]": "word",
	}
	for name, value := range want {
		if fields[name] != value {
			t.Errorf("field %s = %q, want %q", name, fields[name], value)
		}
	}
}

func TestTranscribe_A13_JSON(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{
		Body: `{"text":"  Hi there.  ","usage":{"seconds":1.5,"cost":0.0001}}`,
	})
	got, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "openai/whisper-1", Audio: []byte{1}, Format: voice.FormatMP3,
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.Text != "Hi there." {
		t.Errorf("text = %q, want it trimmed", got.Text)
	}
	if got.Duration != 1.5 {
		t.Errorf("duration = %v, want the usage seconds", got.Duration)
	}
	if got.Usage.CostUSD != 0.0001 || got.Usage.CostSource != "provider" {
		t.Errorf("usage = %+v", got.Usage)
	}
	if got.Usage.PromptTokens != 0 || got.Usage.TotalTokens != 0 {
		t.Errorf("usage invented token counts: %+v", got.Usage)
	}
}

func TestTranscribe_A14_Verbose(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Body: `{
		"text":"Hi.",
		"task":"transcribe",
		"language":"english",
		"duration":1,
		"usage":{"seconds":1,"cost":0.0001},
		"segments":[{"start":0,"end":1,"text":"Hi.","speaker":"A"}],
		"words":[{"word":"Hi","start":0,"end":0.75,"confidence":0.9}]
	}`})
	got, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "openai/whisper-1", Audio: []byte{1}, Format: voice.FormatMP3, Verbose: true,
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.Duration != 1 || got.Language != "english" {
		t.Errorf("duration/language = %v/%q", got.Duration, got.Language)
	}
	if len(got.Segments) != 1 || got.Segments[0].Speaker != "A" || got.Segments[0].Text != "Hi." {
		t.Errorf("segments = %+v", got.Segments)
	}
	if len(got.Words) != 1 || got.Words[0].Word != "Hi" || got.Words[0].Confidence == nil || *got.Words[0].Confidence != 0.9 {
		t.Errorf("words = %+v", got.Words)
	}
}

func TestTranscribe_A15_EmptyTextIsAnAnswer(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Body: `{"text":"   "}`})
	got, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "openai/whisper-1", Audio: []byte{1}, Format: voice.FormatMP3,
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.Text != "" {
		t.Errorf("text = %q, want empty", got.Text)
	}
}

func TestTranscribe_A17_TimestampsNeedVerbose(t *testing.T) {
	client, srv := newClient(t)
	if _, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "openai/whisper-1", Audio: []byte{1}, Format: voice.FormatMP3,
		Timestamps: []string{"word"},
	}); err == nil {
		t.Fatal("Transcribe() = nil, want a local validation error")
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

func TestList_A19(t *testing.T) {
	client, srv := newClient(t, testkit.VoiceServerTurn{Body: `{"data":[
		{"id":"hexgrad/kokoro-82m","name":"Kokoro","context_length":0,
		 "pricing":{"prompt":"0.00000062"},
		 "architecture":{"input_modalities":["text"],"output_modalities":["speech"]},
		 "supported_voices":["af_heart","am_adam"]},
		{"id":"vendor/other","name":"Other","architecture":{}}
	]}`})
	models, err := client.List(context.Background(), voice.KindSpeech)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}
	if m := models[0]; m.ID != "hexgrad/kokoro-82m" || m.Provider != "openrouter" || m.InPerM != 0.62 || !m.SupportsSpeech() {
		t.Errorf("model 0 = %+v", m)
	}
	if len(models[0].Voices) != 2 || models[0].Voices[0] != "af_heart" {
		t.Errorf("voices = %v", models[0].Voices)
	}
	if !models[1].SupportsSpeech() {
		t.Errorf("a filtered entry that omits the modality lost it: %+v", models[1])
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Method != "GET" || reqs[0].Path != "/models" {
		t.Fatalf("requests = %+v", reqs)
	}
	if reqs[0].Query != "output_modalities=speech" {
		t.Errorf("query = %q, want output_modalities=speech", reqs[0].Query)
	}
}

func TestList_A19_Transcription(t *testing.T) {
	client, srv := newClient(t, testkit.VoiceServerTurn{Body: `{"data":[{"id":"openai/whisper-1"}]}`})
	models, err := client.List(context.Background(), voice.KindTranscription)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) != 1 || !models[0].SupportsTranscription() {
		t.Errorf("models = %+v", models)
	}
	if got := srv.Requests()[0].Query; got != "output_modalities=transcription" {
		t.Errorf("query = %q, want output_modalities=transcription", got)
	}
}

func TestList_A19_RejectsAnUnknownKind(t *testing.T) {
	client, _ := newClient(t)
	if _, err := client.List(context.Background(), voice.Kind("images")); err == nil {
		t.Fatal("List() = nil, want an error for an unknown kind")
	}
}

func TestSpeak_KeyError(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{1}})
	client := openrouter.New(openrouter.Config{
		BaseURL: srv.URL,
		APIKeyFunc: func(context.Context) (string, error) {
			return "", errors.New("no credential")
		},
	})
	if _, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi."}); err == nil {
		t.Fatal("Speak() = nil, want the key error")
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}
}

func TestSpeak_A10_NoGenerationIDMeansNoLookup(t *testing.T) {
	client, srv := newCostClient(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	clip, err := client.Speak(context.Background(), voice.SpeechRequest{Model: "hexgrad/kokoro-82m", Voice: "af_heart", Input: "Hi."})
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if clip.Usage.CostUSD != 0 || clip.Usage.CostSource != "" {
		t.Errorf("usage = %+v", clip.Usage)
	}
	if got := len(srv.Requests()); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestTranscribe_A05_ProviderError(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Status: 400, Body: `{"error":{"message":"bad audio"}}`})
	_, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "openai/whisper-1", Audio: []byte{1}, Format: voice.FormatMP3,
	})
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) || providerErr.Status != 400 {
		t.Fatalf("error = %v, want a 400 provider error", err)
	}
}

func TestTranscribe_A13_NoUsageBlock(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Body: `{"text":"Hi."}`})
	got, err := client.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "openai/whisper-1", Audio: []byte{1}, Format: voice.FormatMP3,
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.Usage.CostSource != "" {
		t.Errorf("an absent usage block reported a source: %+v", got.Usage)
	}
}

func TestList_A05_ProviderError(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Status: 502, Body: `{"error":{"message":"upstream"}}`})
	if _, err := client.List(context.Background(), voice.KindSpeech); err == nil {
		t.Fatal("List() = nil, want an error")
	}
}

func TestList_MalformedCatalog(t *testing.T) {
	client, _ := newClient(t, testkit.VoiceServerTurn{Body: `not json`})
	if _, err := client.List(context.Background(), voice.KindSpeech); err == nil {
		t.Fatal("List() = nil, want an error")
	}
}
