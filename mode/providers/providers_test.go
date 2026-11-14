package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/voice"
)

// recordServer counts requests.
type recordServer struct {
	*httptest.Server
	mu      sync.Mutex
	paths   []string
	headers []http.Header
}

func newRecordServer(t *testing.T) *recordServer {
	t.Helper()
	rs := &recordServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.paths = append(rs.paths, r.URL.Path)
		rs.headers = append(rs.headers, r.Header.Clone())
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordServer) count() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return len(rs.paths)
}

// lastHeader returns a header of the most recent request, or "" when there was none.
func (rs *recordServer) lastHeader(name string) string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.headers) == 0 {
		return ""
	}
	return rs.headers[len(rs.headers)-1].Get(name)
}

func envFrom(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestProviders_OPG12_ConfigKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.APIKey = "  cfg-key  "
	key, source, err := ResolveOpenCodeGoKey(&cfg, envFrom(nil))
	if err != nil || key != "cfg-key" || source != "config" {
		t.Fatalf("key=%q source=%q err=%v", key, source, err)
	}
}

func TestProviders_OPG13_EnvKey(t *testing.T) {
	cfg := config.Defaults()
	key, source, err := ResolveOpenCodeGoKey(&cfg, envFrom(map[string]string{auth.EnvOpenCodeGoKey: " env-key "}))
	if err != nil || key != "env-key" || source != "env" {
		t.Fatalf("key=%q source=%q err=%v", key, source, err)
	}
}

func TestProviders_OPG14_ConfigBeatsEnv(t *testing.T) {
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.APIKey = "cfg"
	key, source, _ := ResolveOpenCodeGoKey(&cfg, envFrom(map[string]string{auth.EnvOpenCodeGoKey: "env"}))
	if key != "cfg" || source != "config" {
		t.Fatalf("key=%q source=%q", key, source)
	}
}

func TestProviders_OPG15_NoKey(t *testing.T) {
	cfg := config.Defaults()
	_, _, err := ResolveOpenCodeGoKey(&cfg, envFrom(nil))
	if !errors.Is(err, auth.ErrNoKey) {
		t.Fatalf("err = %v, want ErrNoKey", err)
	}
}

func TestProviders_OPG16_RouterPicksOpenCodeGo(t *testing.T) {
	goSrv, orSrv := newRecordServer(t), newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(nil)})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
	if goSrv.count() == 0 {
		t.Fatal("the OpenCode Go server received no request")
	}
	if orSrv.count() != 0 {
		t.Fatal("the OpenRouter server received the request")
	}
}

func TestProviders_OPG17_RouterPicksOpenRouter(t *testing.T) {
	goSrv, orSrv := newRecordServer(t), newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"})})

	model := openRouterModel()
	_, _ = p.Stream(context.Background(), provider.Request{Model: model}, nil)
	if orSrv.count() == 0 {
		t.Fatalf("the OpenRouter server received no request for %s", model)
	}
	if goSrv.count() != 0 {
		t.Fatal("the OpenCode Go server received the request")
	}
}

func TestProviders_OPG18_UnknownFallsBack(t *testing.T) {
	goSrv, orSrv := newRecordServer(t), newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"})})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "not-in-any-catalog"}, nil)
	if orSrv.count() == 0 {
		t.Fatal("an unknown model did not fall back to OpenRouter")
	}
}

func TestProviders_OPG19_BaseURLOverride(t *testing.T) {
	t.Run("config wins", func(t *testing.T) {
		cfgSrv, envSrv := newRecordServer(t), newRecordServer(t)
		cfg := config.Defaults()
		cfg.Provider.OpenCodeGo.BaseURL = cfgSrv.URL
		p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"NOVA_OPENCODE_GO_BASE_URL": envSrv.URL})})
		_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
		if cfgSrv.count() == 0 || envSrv.count() != 0 {
			t.Fatalf("config=%d env=%d, want config", cfgSrv.count(), envSrv.count())
		}
	})
	t.Run("env when config empty", func(t *testing.T) {
		cfgSrv, envSrv := newRecordServer(t), newRecordServer(t)
		cfg := config.Defaults()
		_ = cfgSrv
		p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"NOVA_OPENCODE_GO_BASE_URL": envSrv.URL})})
		_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
		if envSrv.count() == 0 {
			t.Fatal("the env base URL was not used")
		}
	})
}

// openRouterModel returns a catalog model served by OpenRouter.
func openRouterModel() string {
	for _, m := range provider.Models() {
		if m.Provider == "openrouter" {
			return m.ID
		}
	}
	return "openrouter/auto"
}

func TestProviders_TMO11_AgentRetriesTimedOutDialect(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"index\":0}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = srv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(nil)})

	clock := testkit.NewFakeClock(time.Now())
	var retries int
	a := agent.New(agent.Config{
		Provider: p, Model: "opencode-go/glm-5.3", Clock: clock,
		Retry: agent.RetryPolicy{Max: 2, Base: time.Second, Jitter: 0},
		Emit: func(ev proto.Event) {
			if ev.Type == proto.EventRetry {
				retries++
			}
		},
	})
	done := make(chan struct{})
	go func() { defer close(done); _, _ = a.Run(context.Background(), nil, message.User("go")) }()
	clock.BlockUntil(t, 1)
	clock.Advance(time.Hour)
	<-done
	if retries != 1 {
		t.Fatalf("retry events = %d, want 1", retries)
	}
}

// sessionRouter builds a router whose conversation id is whatever *id holds,
// with both servers recording.
func sessionRouter(t *testing.T, id *string) (provider.Provider, *recordServer, *recordServer) {
	t.Helper()
	goSrv, orSrv := newRecordServer(t), newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{
		Config: &cfg, Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"}),
		SessionID: func() string { return *id },
	})
	return p, goSrv, orSrv
}

func TestProviders_OPG23_OpenCodeGoGetsTheConversationID(t *testing.T) {
	id := "ses_one"
	p, goSrv, _ := sessionRouter(t, &id)
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
	if got := goSrv.lastHeader("x-opencode-session"); got != "ses_one" {
		t.Fatalf("x-opencode-session = %q, want ses_one", got)
	}
}

func TestProviders_OPG24_OpenRouterDoesNotGetIt(t *testing.T) {
	id := "ses_one"
	p, _, orSrv := sessionRouter(t, &id)
	_, _ = p.Stream(context.Background(), provider.Request{Model: openRouterModel()}, nil)
	if orSrv.count() == 0 {
		t.Fatal("the OpenRouter server received no request")
	}
	if got := orSrv.lastHeader("x-opencode-session"); got != "" {
		t.Fatalf("OpenRouter was sent x-opencode-session = %q", got)
	}
}

func TestProviders_OPG25_TheHeaderFollowsTheConversation(t *testing.T) {
	id := "ses_one"
	p, goSrv, _ := sessionRouter(t, &id)
	req := provider.Request{Model: "opencode-go/glm-5.3"}
	_, _ = p.Stream(context.Background(), req, nil)
	id = "ses_two" // session.new, resume, or fork changed the conversation
	_, _ = p.Stream(context.Background(), req, nil)
	if got := goSrv.lastHeader("x-opencode-session"); got != "ses_two" {
		t.Fatalf("after the conversation changed, header = %q, want ses_two", got)
	}
}

func TestProviders_OPG26_NoIDNoHeader(t *testing.T) {
	id := ""
	p, goSrv, _ := sessionRouter(t, &id)
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3"}, nil)
	if goSrv.count() == 0 {
		t.Fatal("no request reached the server")
	}
	if got := goSrv.lastHeader("x-opencode-session"); got != "" {
		t.Fatalf("an empty id still sent the header: %q", got)
	}
}

func TestProviders_OPG27_CallersHeadersAreKeptAndNotMutated(t *testing.T) {
	id := "ses_one"
	p, goSrv, _ := sessionRouter(t, &id)
	extra := map[string]string{"X-Custom": "kept"}
	_, _ = p.Stream(context.Background(), provider.Request{Model: "opencode-go/glm-5.3", ExtraHeaders: extra}, nil)
	if got := goSrv.lastHeader("X-Custom"); got != "kept" {
		t.Fatalf("X-Custom = %q, the caller's header was lost", got)
	}
	if _, leaked := extra["x-opencode-session"]; leaked || len(extra) != 1 {
		t.Fatalf("the caller's ExtraHeaders map was mutated: %v", extra)
	}
}

func TestProviders_OPG33_RouterUsesTheStableModelID(t *testing.T) {
	// A server sends the wire slug as Model ("glm-5.3") and the catalog id as
	// ModelID. The slug alone does not name its provider, so routing by it sent
	// every OpenCode Go model to OpenRouter.
	goSrv, orSrv := newRecordServer(t), newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"})})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "glm-5.3", ModelID: "opencode-go/glm-5.3"}, nil)
	if goSrv.count() == 0 || orSrv.count() != 0 {
		t.Fatalf("OpenCode Go got %d requests and OpenRouter got %d, want the request on OpenCode Go only", goSrv.count(), orSrv.count())
	}
}

func TestProviders_OPG37_SingleAttemptNoSpill(t *testing.T) {
	// Nova has no cross-provider model mapping, so its routes are single
	// attempts: a Go failure returns, it never spends a second backend (#59).
	goSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	t.Cleanup(goSrv.Close)
	orSrv := newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"})})
	_, err := p.Stream(context.Background(), provider.Request{Model: "glm-5.3", ModelID: "opencode-go/glm-5.3"}, nil)
	if err == nil {
		t.Fatal("Stream succeeded, want the Go error")
	}
	if orSrv.count() != 0 {
		t.Errorf("OpenRouter saw %d requests, want none", orSrv.count())
	}
}

func TestProviders_OPG34_AnUnknownModelIDStillFallsBackToOpenRouter(t *testing.T) {
	goSrv, orSrv := newRecordServer(t), newRecordServer(t)
	cfg := config.Defaults()
	cfg.Provider.OpenCodeGo.BaseURL = goSrv.URL
	cfg.Provider.OpenRouter.BaseURL = orSrv.URL
	p := Build(Options{Config: &cfg, Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"})})
	_, _ = p.Stream(context.Background(), provider.Request{Model: "x/y", ModelID: "nobody/never-heard-of-it"}, nil)
	if orSrv.count() == 0 || goSrv.count() != 0 {
		t.Fatalf("OpenRouter got %d and OpenCode Go got %d, want OpenRouter only", orSrv.count(), goSrv.count())
	}
}

func floatPtr(f float64) *float64 { return &f }

func boolPtr(b bool) *bool { return &b }

// TestBuildSpeaker_A07_RoleTable proves the config's speech role reaches the
// wire: the model, the voice, the format and the speed all come from the role.
func TestBuildSpeaker_A07_RoleTable(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	cfg := &config.Config{}
	cfg.Provider.OpenRouter.BaseURL = srv.URL
	cfg.Voice.CostLookup = boolPtr(false) // the estimate is not this test's subject
	cfg.Voice.Speech.Roles = map[string]config.SpeechRoleConfig{
		"narrate": {
			Model: "vendor/tts", Voice: "af_heart", Format: "mp3", Speed: floatPtr(1.2),
			Instructions: "calm", TimeoutMS: 5000,
		},
	}
	speaker := BuildSpeaker(Options{
		Config: cfg,
		Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"}),
	})
	if _, err := speaker.Speak(context.Background(), voice.SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	var body struct {
		Model          string   `json:"model"`
		Voice          string   `json:"voice"`
		ResponseFormat string   `json:"response_format"`
		Speed          *float64 `json:"speed"`
		Instructions   string   `json:"instructions"`
	}
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if body.Model != "vendor/tts" || body.Voice != "af_heart" || body.ResponseFormat != "mp3" || body.Instructions != "calm" {
		t.Errorf("body = %+v", body)
	}
	if body.Speed == nil || *body.Speed != 1.2 {
		t.Errorf("speed = %v, want 1.2", body.Speed)
	}
}

// TestBuildSpeaker_A33_RolePinReachesTheWire: a role's provider pin is a config
// key, so `voice.speech.roles.narrate.provider.only` is all an operator writes to
// route a synthesis away from the cheapest default. The fallback carries its own.
func TestBuildSpeaker_A33_RolePinReachesTheWire(t *testing.T) {
	srv := testkit.NewVoiceServer(t,
		testkit.VoiceServerTurn{Status: 429, Body: `{"error":{"message":"busy"}}`},
		testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"},
	)
	cfg := &config.Config{}
	cfg.Provider.OpenRouter.BaseURL = srv.URL
	cfg.Voice.CostLookup = boolPtr(false)
	cfg.Voice.Speech.Roles = map[string]config.SpeechRoleConfig{
		"narrate": {
			Model: "hexgrad/kokoro-82m", Voice: "af_heart",
			Provider: map[string]any{"only": []string{"Together"}},
			Fallback: &config.SpeechFallbackConfig{
				Model: "fallback/tts", Voice: "two",
				Provider: map[string]any{"only": []string{"DeepInfra"}},
			},
		},
	}
	speaker := BuildSpeaker(Options{
		Config: cfg,
		Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"}),
	})
	if _, err := speaker.Speak(context.Background(), voice.SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	reqs := srv.Requests()
	var speech []testkit.RecordedRequest
	for _, r := range reqs {
		if r.Path == "/audio/speech" {
			speech = append(speech, r)
		}
	}
	if len(speech) != 2 {
		t.Fatalf("speech requests = %d, want 2", len(speech))
	}
	if got := speechPin(t, speech[0].Body); !sameOnly(got, "Together") {
		t.Errorf("primary provider = %v, want only Together", got)
	}
	if got := speechPin(t, speech[1].Body); !sameOnly(got, "DeepInfra") {
		t.Errorf("fallback provider = %v, want only DeepInfra", got)
	}
}

// TestBuildSpeaker_A33_NoPinSendsNoProviderKey: a role without a provider sends
// no provider object at all, so the default stays the provider's choice.
func TestBuildSpeaker_A33_NoPinSendsNoProviderKey(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	cfg := &config.Config{}
	cfg.Provider.OpenRouter.BaseURL = srv.URL
	cfg.Voice.CostLookup = boolPtr(false)
	cfg.Voice.Speech.Roles = map[string]config.SpeechRoleConfig{
		"narrate": {Model: "hexgrad/kokoro-82m", Voice: "af_heart"},
	}
	speaker := BuildSpeaker(Options{
		Config: cfg,
		Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"}),
	})
	if _, err := speaker.Speak(context.Background(), voice.SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["provider"] != nil {
		t.Errorf("body = %s, want no provider key", reqs[0].Body)
	}
}

// speechPin decodes the provider object from a recorded speech body, failing the
// test when it is absent.
func speechPin(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var top struct {
		Provider map[string]any `json:"provider"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	if top.Provider == nil {
		t.Fatalf("body = %s, want a provider key", body)
	}
	return top.Provider
}

// sameOnly reports whether pin pins exactly name through "only".
func sameOnly(pin map[string]any, name string) bool {
	only, ok := pin["only"].([]any)
	return ok && len(only) == 1 && only[0] == name
}

// TestBuildTranscriber_A07_RoleTable: a transcription role resolves to its model
// and reaches /audio/transcriptions.
func TestBuildTranscriber_A07_RoleTable(t *testing.T) {
	srv := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Body: `{"text":"hi"}`})
	cfg := &config.Config{}
	cfg.Provider.OpenRouter.BaseURL = srv.URL
	cfg.Voice.Transcribe.Roles = map[string]config.TranscribeRoleConfig{
		"default": {Model: "vendor/stt", TimeoutMS: 5000},
	}
	transcriber := BuildTranscriber(Options{
		Config: cfg,
		Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"}),
	})
	got, err := transcriber.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "default", Audio: []byte{1}, Format: voice.FormatMP3,
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.Text != "hi" || got.Model != "vendor/stt" {
		t.Errorf("transcript = %+v", got)
	}
	if reqs := srv.Requests(); len(reqs) != 1 || reqs[0].Path != "/audio/transcriptions" {
		t.Errorf("requests = %+v", reqs)
	}
}

// TestBuildSpeaker_L02_EndpointWinsOverTheDefault points one role at a local
// engine and leaves another on the harness's own client: the request must land
// on the engine named, with no credential borrowed from the other one.
func TestBuildSpeaker_L02_EndpointWinsOverTheDefault(t *testing.T) {
	local := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	cfg := &config.Config{}
	cfg.Voice.Providers = map[string]config.VoiceProviderConfig{
		"local": {BaseURL: local.URL},
	}
	cfg.Voice.Speech.Roles = map[string]config.SpeechRoleConfig{
		"narrate": {Model: "kokoro", Voice: "af_heart", Endpoint: "local"},
	}
	speaker := BuildSpeaker(Options{
		Config: cfg,
		Getenv: envFrom(map[string]string{"OPENROUTER_API_KEY": "or-key"}),
	})
	if _, err := speaker.Speak(context.Background(), voice.SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	reqs := local.Requests()
	if len(reqs) != 1 {
		t.Fatalf("local requests = %d, want 1", len(reqs))
	}
	if got := reqs[0].Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none: a local engine borrows no key", got)
	}
}

// TestBuildSpeaker_L02_EndpointKeyComesFromItsEnv: a hosted endpoint takes its
// own key, named in config, rather than the harness's OpenRouter credential.
func TestBuildSpeaker_L02_EndpointKeyComesFromItsEnv(t *testing.T) {
	hosted := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Audio: []byte{1}, ContentType: "audio/mpeg"})
	cfg := &config.Config{}
	cfg.Voice.Providers = map[string]config.VoiceProviderConfig{
		"hosted": {BaseURL: hosted.URL, APIKeyEnv: "MY_TTS_KEY"},
	}
	cfg.Voice.Speech.Roles = map[string]config.SpeechRoleConfig{
		"narrate": {Model: "gpt-4o-mini-tts", Voice: "alloy", Endpoint: "hosted"},
	}
	speaker := BuildSpeaker(Options{
		Config: cfg,
		Getenv: envFrom(map[string]string{"MY_TTS_KEY": "sk-endpoint-key-123", "OPENROUTER_API_KEY": "or-key"}),
	})
	if _, err := speaker.Speak(context.Background(), voice.SpeechRequest{Model: "narrate", Input: "hi"}); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if got := hosted.Requests()[0].Header.Get("Authorization"); got != "Bearer sk-endpoint-key-123" {
		t.Errorf("Authorization = %q, want the endpoint's own key", got)
	}
}

// TestBuildTranscriber_L06_LocalRoleNeedsNoKey: a local transcription role
// resolves with nothing in the environment and nothing in the key store.
func TestBuildTranscriber_L06_LocalRoleNeedsNoKey(t *testing.T) {
	local := testkit.NewVoiceServer(t, testkit.VoiceServerTurn{Body: `{"text":"hi"}`})
	cfg := &config.Config{}
	cfg.Voice.Providers = map[string]config.VoiceProviderConfig{
		"local": {BaseURL: local.URL},
	}
	cfg.Voice.Transcribe.Roles = map[string]config.TranscribeRoleConfig{
		"default": {Model: "whisper-1", Endpoint: "local"},
	}
	transcriber := BuildTranscriber(Options{Config: cfg, Getenv: envFrom(nil)})
	got, err := transcriber.Transcribe(context.Background(), voice.TranscribeRequest{
		Model: "default", Audio: []byte{1}, Format: voice.FormatMP3,
	})
	if err != nil {
		t.Fatalf("Transcribe with no key at all: %v", err)
	}
	if got.Text != "hi" {
		t.Errorf("text = %q, want hi", got.Text)
	}
	if got.Usage.CostSource != "" {
		t.Errorf("cost source = %q, want none from a local server", got.Usage.CostSource)
	}
}

// TestBuildSpeaker_EndpointWithoutBaseURLIsNotA Client: an endpoint the config
// refused to warn about is one with no address, so the role that names it fails
// instead of quietly calling OpenAI's own API.
func TestBuildSpeaker_EndpointWithoutBaseURLFails(t *testing.T) {
	cfg := &config.Config{}
	cfg.Voice.Providers = map[string]config.VoiceProviderConfig{"broken": {}}
	cfg.Voice.Speech.Roles = map[string]config.SpeechRoleConfig{
		"narrate": {Model: "kokoro", Voice: "af_heart", Endpoint: "broken"},
	}
	speaker := BuildSpeaker(Options{Config: cfg, Getenv: envFrom(nil)})
	_, err := speaker.Speak(context.Background(), voice.SpeechRequest{Model: "narrate", Input: "hi"})
	if err == nil {
		t.Fatal("Speak() = nil, want an unknown endpoint error rather than a call to api.openai.com")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error = %v, want it to name the endpoint", err)
	}
}
