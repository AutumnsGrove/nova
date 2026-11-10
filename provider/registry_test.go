package provider

import (
	"context"
	"testing"
)

// namedProvider is a do-nothing Provider for registry tests.
type namedProvider struct{ name string }

func (p namedProvider) Name() string { return p.name }

func (p namedProvider) Stream(context.Context, Request, func(StreamEvent)) (*Response, error) {
	return nil, nil
}

// mustPanic runs fn and fails the test unless it panics.
func mustPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", what)
		}
	}()
	fn()
}

func TestProvider_PRV16_RegistryDuplicatePanics(t *testing.T) {
	Register(namedProvider{name: "prv16"})
	mustPanic(t, "duplicate registration", func() { Register(namedProvider{name: "prv16"}) })
}

func TestProvider_PRV17_RegistryNilOrUnnamedPanics(t *testing.T) {
	mustPanic(t, "nil registration", func() { Register(nil) })
	mustPanic(t, "unnamed registration", func() { Register(namedProvider{}) })
}

func TestProvider_PRV18_RegistryGetAndNames(t *testing.T) {
	Register(namedProvider{name: "prv18-b"})
	Register(namedProvider{name: "prv18-a"})
	if _, ok := Get("prv18-missing"); ok {
		t.Error("Get of a missing provider returned true")
	}
	names := Names()
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("Names not sorted: %v", names)
		}
	}
}

func TestProvider_PRV19_RegisterModel(t *testing.T) {
	mustPanic(t, "empty model id", func() { RegisterModel(Model{Provider: "x"}) })
	RegisterModel(Model{ID: "prv19", Name: "first", Provider: "x"})
	RegisterModel(Model{ID: "prv19", Name: "second", Provider: "x"})
	m, ok := GetModel("prv19")
	if !ok {
		t.Fatal("GetModel(prv19) not found")
	}
	if m.Name != "second" {
		t.Errorf("re-registered model Name = %q, want second", m.Name)
	}
}

func TestProvider_PRV27_ModelKeepsPickerFields(t *testing.T) {
	RegisterModel(Model{
		ID: "prv27", Provider: "x",
		ReasoningEfforts: []string{"low", "max"},
		ToolChoice:       []string{"auto", "required"},
		PriceTiers:       []PriceTier{{AboveTokens: 200000, InPerM: 4, OutPerM: 12}},
		CostMultipliers:  []CostMultiplier{{Factor: 2, Window: "weekday peaks"}},
		MonthlyLimitUSD:  100,
	})
	m, ok := GetModel("prv27")
	if !ok {
		t.Fatal("GetModel(prv27) not found")
	}
	if len(m.ReasoningEfforts) != 2 || m.ReasoningEfforts[1] != "max" {
		t.Errorf("ReasoningEfforts = %v", m.ReasoningEfforts)
	}
	if len(m.ToolChoice) != 2 || m.ToolChoice[1] != "required" {
		t.Errorf("ToolChoice = %v", m.ToolChoice)
	}
	if len(m.PriceTiers) != 1 || m.PriceTiers[0] != (PriceTier{AboveTokens: 200000, InPerM: 4, OutPerM: 12}) {
		t.Errorf("PriceTiers = %v", m.PriceTiers)
	}
	if len(m.CostMultipliers) != 1 || m.CostMultipliers[0] != (CostMultiplier{Factor: 2, Window: "weekday peaks"}) {
		t.Errorf("CostMultipliers = %v", m.CostMultipliers)
	}
	if m.MonthlyLimitUSD != 100 {
		t.Errorf("MonthlyLimitUSD = %v", m.MonthlyLimitUSD)
	}
}

func TestProvider_PRV20_ModelsSorted(t *testing.T) {
	RegisterModel(Model{ID: "prv20-b", Provider: "zprov"})
	RegisterModel(Model{ID: "prv20-a", Provider: "aprov"})
	RegisterModel(Model{ID: "prv20-c", Provider: "aprov"})
	got := Models()
	for i := 1; i < len(got); i++ {
		if got[i-1].Provider > got[i].Provider {
			t.Fatalf("Models not sorted by provider: %v", got)
		}
		if got[i-1].Provider == got[i].Provider && got[i-1].ID > got[i].ID {
			t.Fatalf("Models not sorted by id within a provider: %v", got)
		}
	}
}

func TestProvider_PRV21_AcceptsImages(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want bool
	}{
		{"nil is text only", nil, false},
		{"text only", []string{"text"}, false},
		{"image only", []string{"image"}, true},
		{"text and image", []string{"text", "image"}, true},
		{"unrelated kinds", []string{"text", "file"}, false},
	}
	for _, tc := range cases {
		if got := (Model{InputModalities: tc.in}).AcceptsImages(); got != tc.want {
			t.Errorf("%s: AcceptsImages() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProvider_PRV22_ModelKeepsInputModalities(t *testing.T) {
	RegisterModel(Model{ID: "prv22", Provider: "x", InputModalities: []string{"text", "image"}})
	got, ok := GetModel("prv22")
	if !ok {
		t.Fatal("GetModel(prv22) not found")
	}
	if !got.AcceptsImages() {
		t.Errorf("registered model lost its modalities: %+v", got)
	}
}

func TestProvider_PRV23_SupportsDecisions(t *testing.T) {
	cases := []struct {
		name string
		out  []string
		want bool
	}{
		{"nil is a chat model", nil, false},
		{"text only", []string{"text"}, false},
		{"decisions only", []string{"decisions"}, true},
		{"text and decisions", []string{"text", "decisions"}, true},
		{"unrelated kinds", []string{"text", "embedding"}, false},
	}
	for _, tc := range cases {
		if got := (Model{OutputModalities: tc.out}).SupportsDecisions(); got != tc.want {
			t.Errorf("%s: SupportsDecisions() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProvider_PRV24_ModelKeepsOutputModalities(t *testing.T) {
	RegisterModel(Model{ID: "prv24", Provider: "x", OutputModalities: []string{"decisions"}})
	got, ok := GetModel("prv24")
	if !ok {
		t.Fatal("GetModel(prv24) not found")
	}
	if !got.SupportsDecisions() {
		t.Errorf("registered model lost its output modalities: %+v", got)
	}
}

func TestProvider_EMB18_SupportsEmbeddings(t *testing.T) {
	cases := []struct {
		name string
		out  []string
		want bool
	}{
		{"nil is a chat model", nil, false},
		{"text only", []string{"text"}, false},
		{"embeddings only", []string{"embeddings"}, true},
		{"text and embeddings", []string{"text", "embeddings"}, true},
		{"decisions only", []string{"decisions"}, false},
	}
	for _, tc := range cases {
		if got := (Model{OutputModalities: tc.out}).SupportsEmbeddings(); got != tc.want {
			t.Errorf("%s: SupportsEmbeddings() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProvider_A19_SupportsSpeechAndTranscription(t *testing.T) {
	cases := []struct {
		name          string
		out           []string
		speech        bool
		transcription bool
	}{
		{"nil is a chat model", nil, false, false},
		{"text only", []string{"text"}, false, false},
		{"speech only", []string{"speech"}, true, false},
		{"transcription only", []string{"transcription"}, false, true},
		{"embeddings are neither", []string{"embeddings"}, false, false},
	}
	for _, tc := range cases {
		m := Model{OutputModalities: tc.out}
		if got := m.SupportsSpeech(); got != tc.speech {
			t.Errorf("%s: SupportsSpeech() = %v, want %v", tc.name, got, tc.speech)
		}
		if got := m.SupportsTranscription(); got != tc.transcription {
			t.Errorf("%s: SupportsTranscription() = %v, want %v", tc.name, got, tc.transcription)
		}
	}
}

func TestProvider_A19_ModelKeepsVoices(t *testing.T) {
	RegisterModel(Model{ID: "a19", Provider: "x", OutputModalities: []string{"speech"}, Voices: []string{"af_heart", "am_adam"}})
	got, ok := GetModel("a19")
	if !ok {
		t.Fatal("GetModel(a19) not found")
	}
	if !got.SupportsSpeech() {
		t.Errorf("registered model lost its output modalities: %+v", got)
	}
	if len(got.Voices) != 2 || got.Voices[0] != "af_heart" || got.Voices[1] != "am_adam" {
		t.Errorf("registered model lost its voices: %+v", got.Voices)
	}
}
