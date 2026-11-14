package voice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
)

func floatPtr(f float64) *float64 { return &f }

type speakStep struct {
	clip *Clip
	err  error
}

type fakeSpeaker struct {
	calls []SpeechRequest
	steps []speakStep
}

func (f *fakeSpeaker) Speak(_ context.Context, req SpeechRequest) (*Clip, error) {
	f.calls = append(f.calls, req)
	if len(f.calls) > len(f.steps) {
		return nil, fmt.Errorf("fakeSpeaker: unexpected call %d", len(f.calls))
	}
	step := f.steps[len(f.calls)-1]
	return step.clip, step.err
}

type transcribeStep struct {
	transcript *Transcript
	err        error
}

type fakeTranscriber struct {
	calls []TranscribeRequest
	steps []transcribeStep
}

func (f *fakeTranscriber) Transcribe(_ context.Context, req TranscribeRequest) (*Transcript, error) {
	f.calls = append(f.calls, req)
	if len(f.calls) > len(f.steps) {
		return nil, fmt.Errorf("fakeTranscriber: unexpected call %d", len(f.calls))
	}
	step := f.steps[len(f.calls)-1]
	return step.transcript, step.err
}

func TestSpeak_A07_RoleResolves(t *testing.T) {
	fake := &fakeSpeaker{steps: []speakStep{{clip: &Clip{Bytes: []byte("x"), Format: FormatMP3, Voice: "af_heart"}}}}
	roles := NewRoles(RolesConfig{
		Speaker: fake,
		Speech: map[string]VoiceRole{
			"narrate": {Model: "hexgrad/kokoro-82m", Voice: "af_heart", Format: FormatMP3, Speed: floatPtr(1.2), Instructions: "calm"},
		},
	})
	got, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"})
	if err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("speaker calls = %d, want 1", len(fake.calls))
	}
	call := fake.calls[0]
	if call.Model != "hexgrad/kokoro-82m" || call.Voice != "af_heart" || call.Format != FormatMP3 || call.Speed == nil || *call.Speed != 1.2 || call.Instructions != "calm" {
		t.Errorf("resolved request = %+v", call)
	}
	if got.Voice != "af_heart" {
		t.Errorf("clip voice = %q, want af_heart", got.Voice)
	}
}

func TestSpeak_A07_RequestWinsOverRole(t *testing.T) {
	fake := &fakeSpeaker{steps: []speakStep{{clip: &Clip{}}}}
	roles := NewRoles(RolesConfig{
		Speaker: fake,
		Speech:  map[string]VoiceRole{"narrate": {Model: "m/tts", Voice: "role-voice", Format: FormatMP3}},
	})
	_, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi", Voice: "call-voice", Format: FormatPCM})
	if err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if call := fake.calls[0]; call.Voice != "call-voice" || call.Format != FormatPCM {
		t.Errorf("request overrides were lost: %+v", call)
	}
}

func TestSpeak_A07_DirectIDPassesThrough(t *testing.T) {
	fake := &fakeSpeaker{steps: []speakStep{{clip: &Clip{}}}}
	roles := NewRoles(RolesConfig{Speaker: fake})
	if _, err := roles.Speak(context.Background(), SpeechRequest{Model: "openai/gpt-4o-mini-tts", Voice: "alloy", Input: "hi"}); err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if call := fake.calls[0]; call.Model != "openai/gpt-4o-mini-tts" || call.Voice != "alloy" {
		t.Errorf("direct id was rewritten: %+v", call)
	}
}

func TestSpeak_A07_NoVoiceIsRefusedBeforeTheWire(t *testing.T) {
	fake := &fakeSpeaker{}
	roles := NewRoles(RolesConfig{Speaker: fake, Speech: map[string]VoiceRole{"silent": {Model: "m/tts"}}})
	_, err := roles.Speak(context.Background(), SpeechRequest{Model: "silent", Input: "hi"})
	if err == nil {
		t.Fatal("Speak() = nil, want an error for a missing voice")
	}
	if len(fake.calls) != 0 {
		t.Errorf("speaker was called %d times, want 0", len(fake.calls))
	}
}

func TestSpeak_A08_FallbackRestatesItsVoice(t *testing.T) {
	fake := &fakeSpeaker{steps: []speakStep{
		{clip: &Clip{Usage: provider.Usage{CostUSD: 0.01, CostSource: "provider"}}, err: errors.New("502")},
		{clip: &Clip{Usage: provider.Usage{CostUSD: 0.02, CostSource: "provider"}}},
	}}
	roles := NewRoles(RolesConfig{
		Speaker: fake,
		Speech: map[string]VoiceRole{
			"narrate": {
				Model: "primary/tts", Voice: "one", Format: FormatMP3,
				Fallback: &VoiceAttempt{Model: "fallback/tts", Voice: "two", Format: FormatPCM},
			},
		},
	})
	got, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"})
	if err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("speaker calls = %d, want 2", len(fake.calls))
	}
	if c := fake.calls[0]; c.Model != "primary/tts" || c.Voice != "one" || c.Format != FormatMP3 {
		t.Errorf("primary call = %+v", c)
	}
	if c := fake.calls[1]; c.Model != "fallback/tts" || c.Voice != "two" || c.Format != FormatPCM {
		t.Errorf("fallback call = %+v", c)
	}
	if got.Usage.CostUSD != 0.03 {
		t.Errorf("usage cost = %v, want 0.03 from both attempts", got.Usage.CostUSD)
	}
}

// TestSpeak_A32_RolePinReachesTheWire: a role's provider pin rides on the
// attempt it belongs to, and a fallback's own pin replaces it. The pin cannot
// be inherited for the same reason the voice cannot: it names endpoints of one
// model, and a fallback is another model.
func TestSpeak_A32_RolePinReachesTheWire(t *testing.T) {
	primaryPin := map[string]any{"only": []string{"Together"}}
	fallbackPin := map[string]any{"only": []string{"DeepInfra"}}
	fake := &fakeSpeaker{steps: []speakStep{
		{clip: &Clip{}, err: errors.New("429")},
		{clip: &Clip{}},
	}}
	roles := NewRoles(RolesConfig{
		Speaker: fake,
		Speech: map[string]VoiceRole{
			"narrate": {
				Model: "hexgrad/kokoro-82m", Voice: "af_heart", Provider: primaryPin,
				Fallback: &VoiceAttempt{Model: "fallback/tts", Voice: "two", Provider: fallbackPin},
			},
		},
	})
	if _, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("speaker calls = %d, want 2", len(fake.calls))
	}
	if got := fake.calls[0].Provider; len(got) != 1 || !hasOnly(got, "Together") {
		t.Errorf("primary pin = %v, want only Together", got)
	}
	if got := fake.calls[1].Provider; len(got) != 1 || !hasOnly(got, "DeepInfra") {
		t.Errorf("fallback pin = %v, want only DeepInfra", got)
	}
}

// TestSpeak_A32_NoPinSendsNothing: a role without a pin leaves the request as it
// was, so the provider chooses. The zero value must change nothing.
func TestSpeak_A32_NoPinSendsNothing(t *testing.T) {
	fake := &fakeSpeaker{steps: []speakStep{{clip: &Clip{}}}}
	roles := NewRoles(RolesConfig{
		Speaker: fake,
		Speech: map[string]VoiceRole{
			"narrate": {Model: "hexgrad/kokoro-82m", Voice: "af_heart"},
		},
	})
	if _, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("speaker calls = %d, want 1", len(fake.calls))
	}
	if got := fake.calls[0].Provider; got != nil {
		t.Errorf("provider = %v, want nil so the key is omitted", got)
	}
}

// hasOnly reports whether a provider object pins a single name through "only".
func hasOnly(pin map[string]any, name string) bool {
	only, ok := pin["only"].([]string)
	return ok && len(only) == 1 && only[0] == name
}

func TestSpeak_A08_BadFallbackIsRefusedBeforeThePrimary(t *testing.T) {
	fake := &fakeSpeaker{steps: []speakStep{{clip: &Clip{}}}}
	roles := NewRoles(RolesConfig{
		Speaker: fake,
		Speech: map[string]VoiceRole{
			"narrate": {
				Model: "primary/tts", Voice: "one", Format: FormatMP3,
				Fallback: &VoiceAttempt{Model: "fallback/tts", Voice: "two", Format: FormatOGG},
			},
		},
	})
	if _, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"}); err == nil {
		t.Fatal("Speak() = nil, want a refusal for an unsupported fallback format")
	}
	if len(fake.calls) != 0 {
		t.Errorf("speaker was called %d times, want 0", len(fake.calls))
	}
}

func TestSpeak_A09_Aliases(t *testing.T) {
	fake := &fakeSpeaker{steps: []speakStep{{clip: &Clip{}}, {clip: &Clip{}}}}
	role := map[string]VoiceRole{"narrate": {Model: "vendor/tts-latest", Voice: "v"}}

	strict := NewRoles(RolesConfig{Speaker: fake, Speech: role})
	if _, err := strict.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"}); err == nil {
		t.Fatal("Speak() with an alias = nil, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("speaker was called %d times, want 0", len(fake.calls))
	}

	lenient := NewRoles(RolesConfig{Speaker: fake, Speech: role, AllowAliases: true})
	if _, err := lenient.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
		t.Fatalf("Speak() with allow_aliases = %v", err)
	}
}

func TestSpeak_A09_UnknownRoleNamesTheRole(t *testing.T) {
	roles := NewRoles(RolesConfig{Speaker: &fakeSpeaker{}})
	_, err := roles.Speak(context.Background(), SpeechRequest{Model: "oracle", Input: "hi"})
	if err == nil {
		t.Fatal("Speak() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "oracle") {
		t.Errorf("error %q does not name the role", err)
	}
}

func TestSpeak_NoSpeaker(t *testing.T) {
	roles := NewRoles(RolesConfig{})
	if _, err := roles.Speak(context.Background(), SpeechRequest{Model: "a/b", Voice: "v", Input: "hi"}); err == nil {
		t.Fatal("Speak() = nil, want an error")
	}
}

func TestTranscribe_A18_FallbackAccumulatesUsage(t *testing.T) {
	fake := &fakeTranscriber{steps: []transcribeStep{
		{transcript: &Transcript{Usage: provider.Usage{PromptTokens: 10, CostUSD: 0.001, CostSource: "provider"}}, err: errors.New("503")},
		{transcript: &Transcript{Text: "hello", Usage: provider.Usage{PromptTokens: 20, CostUSD: 0.002, CostSource: "provider"}}},
	}}
	roles := NewRoles(RolesConfig{
		Transcriber: fake,
		Transcribe:  map[string]TranscribeRole{"default": {Model: "primary/stt", Fallback: "fallback/stt"}},
	})
	got, err := roles.Transcribe(context.Background(), TranscribeRequest{Model: "default", Audio: []byte{1}, Format: FormatMP3})
	if err != nil {
		t.Fatalf("Transcribe() = %v", err)
	}
	if got.Text != "hello" {
		t.Errorf("text = %q, want hello", got.Text)
	}
	if got.Usage.PromptTokens != 30 || got.Usage.CostUSD != 0.003 {
		t.Errorf("usage = %+v, want both attempts summed", got.Usage)
	}
	if len(fake.calls) != 2 || fake.calls[0].Model != "primary/stt" || fake.calls[1].Model != "fallback/stt" {
		t.Errorf("calls = %+v", fake.calls)
	}
}

func TestTranscribe_A16_InvalidRequestNeverReachesTheAdapter(t *testing.T) {
	fake := &fakeTranscriber{}
	roles := NewRoles(RolesConfig{Transcriber: fake})
	if _, err := roles.Transcribe(context.Background(), TranscribeRequest{Model: "openai/whisper-1", Format: FormatMP3}); err == nil {
		t.Fatal("Transcribe() = nil, want an error for empty audio")
	}
	if len(fake.calls) != 0 {
		t.Errorf("transcriber was called %d times, want 0", len(fake.calls))
	}
}

func TestTranscribe_NoTranscriber(t *testing.T) {
	roles := NewRoles(RolesConfig{})
	if _, err := roles.Transcribe(context.Background(), TranscribeRequest{Model: "a/b", Audio: []byte{1}, Format: FormatMP3}); err == nil {
		t.Fatal("Transcribe() = nil, want an error")
	}
}

// TestSpeak_A33_EndpointNamesItsSpeaker: a role that names an endpoint is served
// by that endpoint's client, not the default one.
func TestSpeak_A33_EndpointNamesItsSpeaker(t *testing.T) {
	def := &fakeSpeaker{steps: []speakStep{{clip: &Clip{Format: FormatMP3}}}}
	local := &fakeSpeaker{steps: []speakStep{{clip: &Clip{Format: FormatPCM}}}}
	roles := NewRoles(RolesConfig{
		Speaker:  def,
		Speakers: map[string]Speaker{"local": local},
		Speech: map[string]VoiceRole{
			"narrate": {Model: "kokoro", Voice: "af_heart", Endpoint: "local"},
		},
	})
	got, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"})
	if err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if len(local.calls) != 1 || len(def.calls) != 0 {
		t.Fatalf("local calls = %d, default calls = %d, want 1 and 0", len(local.calls), len(def.calls))
	}
	if got.Format != FormatPCM {
		t.Errorf("clip format = %q, want the local speaker's pcm", got.Format)
	}
}

// TestSpeak_A34_NoEndpointUsesTheDefault: the zero value routes as it always
// did, so an existing config sends nothing new.
func TestSpeak_A34_NoEndpointUsesTheDefault(t *testing.T) {
	def := &fakeSpeaker{steps: []speakStep{{clip: &Clip{}}}}
	local := &fakeSpeaker{steps: []speakStep{{clip: &Clip{}}}}
	roles := NewRoles(RolesConfig{
		Speaker:  def,
		Speakers: map[string]Speaker{"local": local},
		Speech: map[string]VoiceRole{
			"narrate": {Model: "openai/gpt-4o-mini-tts", Voice: "alloy"},
		},
	})
	if _, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if len(def.calls) != 1 || len(local.calls) != 0 {
		t.Fatalf("default calls = %d, local calls = %d, want 1 and 0", len(def.calls), len(local.calls))
	}
}

// TestSpeak_A35_FallbackRestatesItsEndpoint: a fallback is served by its own
// endpoint, so a local primary can fall back to a hosted one.
func TestSpeak_A35_FallbackRestatesItsEndpoint(t *testing.T) {
	local := &fakeSpeaker{steps: []speakStep{{err: errors.New("502")}}}
	hosted := &fakeSpeaker{steps: []speakStep{{clip: &Clip{Model: "hosted-tts"}}}}
	roles := NewRoles(RolesConfig{
		Speakers: map[string]Speaker{"local": local, "openai": hosted},
		Speech: map[string]VoiceRole{
			"narrate": {
				Model: "kokoro", Voice: "af_heart", Endpoint: "local",
				Fallback: &VoiceAttempt{Model: "hosted-tts", Voice: "alloy", Endpoint: "openai"},
			},
		},
	})
	got, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"})
	if err != nil {
		t.Fatalf("Speak() = %v", err)
	}
	if len(local.calls) != 1 || len(hosted.calls) != 1 {
		t.Fatalf("local calls = %d, hosted calls = %d, want 1 each", len(local.calls), len(hosted.calls))
	}
	if hosted.calls[0].Model != "hosted-tts" || hosted.calls[0].Voice != "alloy" {
		t.Errorf("fallback call = %+v", hosted.calls[0])
	}
	if got.Model != "hosted-tts" {
		t.Errorf("clip model = %q, want the fallback's", got.Model)
	}
}

// TestSpeak_A36_UnknownEndpointFailsBeforeAnyRequest: a name with no client is a
// configuration error, learned without spending a request.
func TestSpeak_A36_UnknownEndpointFailsBeforeAnyRequest(t *testing.T) {
	def := &fakeSpeaker{steps: []speakStep{{clip: &Clip{}}}}
	roles := NewRoles(RolesConfig{
		Speaker: def,
		Speech: map[string]VoiceRole{
			"narrate": {Model: "kokoro", Voice: "af_heart", Endpoint: "nowhere"},
		},
	})
	_, err := roles.Speak(context.Background(), SpeechRequest{Model: "narrate", Input: "hi"})
	if err == nil {
		t.Fatal("Speak() = nil, want an unknown endpoint error")
	}
	if !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("error = %v, want it to name the endpoint", err)
	}
	if len(def.calls) != 0 {
		t.Errorf("speaker was called %d times, want 0", len(def.calls))
	}
}

// TestTranscribe_A37_EndpointNamesItsTranscriber: the transcription direction
// routes by the same name, because a local whisper server is the other half.
func TestTranscribe_A37_EndpointNamesItsTranscriber(t *testing.T) {
	def := &fakeTranscriber{steps: []transcribeStep{{transcript: &Transcript{Text: "default"}}}}
	local := &fakeTranscriber{steps: []transcribeStep{{transcript: &Transcript{Text: "local"}}}}
	roles := NewRoles(RolesConfig{
		Transcriber:  def,
		Transcribers: map[string]Transcriber{"local": local},
		Transcribe: map[string]TranscribeRole{
			"default": {Model: "whisper-1", Endpoint: "local"},
		},
	})
	got, err := roles.Transcribe(context.Background(), TranscribeRequest{Model: "default", Audio: []byte{1}, Format: FormatMP3})
	if err != nil {
		t.Fatalf("Transcribe() = %v", err)
	}
	if got.Text != "local" {
		t.Errorf("text = %q, want the local transcriber's", got.Text)
	}
	if len(def.calls) != 0 {
		t.Errorf("default transcriber was called %d times, want 0", len(def.calls))
	}
}

// TestTranscribe_A38_UnknownEndpointFailsBeforeAnyRequest mirrors the speech
// direction: a typo costs no request.
func TestTranscribe_A38_UnknownEndpointFailsBeforeAnyRequest(t *testing.T) {
	def := &fakeTranscriber{steps: []transcribeStep{{transcript: &Transcript{Text: "x"}}}}
	roles := NewRoles(RolesConfig{
		Transcriber: def,
		Transcribe: map[string]TranscribeRole{
			"default": {Model: "whisper-1", Endpoint: "nowhere"},
		},
	})
	_, err := roles.Transcribe(context.Background(), TranscribeRequest{Model: "default", Audio: []byte{1}, Format: FormatMP3})
	if err == nil {
		t.Fatal("Transcribe() = nil, want an unknown endpoint error")
	}
	if !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("error = %v, want it to name the endpoint", err)
	}
	if len(def.calls) != 0 {
		t.Errorf("transcriber was called %d times, want 0", len(def.calls))
	}
}
