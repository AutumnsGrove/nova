package openrouter

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/voice"
)

func TestReadBody_A06(t *testing.T) {
	if _, err := readBody(strings.NewReader("12345678"), 8); err != nil {
		t.Fatalf("readBody at the limit = %v", err)
	}
	if _, err := readBody(strings.NewReader("123456789"), 8); err == nil {
		t.Fatal("readBody over the limit = nil, want an error")
	}
}

func TestPCM_A03_Layout(t *testing.T) {
	cases := []struct {
		header                string
		rate, channels, depth int
	}{
		{"audio/pcm;rate=24000;channels=1", 24000, 1, 16},
		{"audio/pcm;rate=48000;channels=2", 48000, 2, 16},
		{"audio/pcm;channels=2", 24000, 2, 16},
		{"audio/pcm", 24000, 1, 16},
		{"audio/pcm;rate=0;channels=-1", 24000, 1, 16},
		{"audio/pcm;rate=abc", 24000, 1, 16},
		{"", 24000, 1, 16},
	}
	for _, tc := range cases {
		rate, channels, depth := pcmLayout(tc.header)
		if rate != tc.rate || channels != tc.channels || depth != tc.depth {
			t.Errorf("pcmLayout(%q) = %d/%d/%d, want %d/%d/%d", tc.header, rate, channels, depth, tc.rate, tc.channels, tc.depth)
		}
	}
}

func TestFormatFromMediaType_A03(t *testing.T) {
	cases := []struct {
		header   string
		fallback voice.Format
		want     voice.Format
	}{
		{"audio/mpeg", voice.FormatPCM, voice.FormatMP3},
		{"audio/mp3", voice.FormatPCM, voice.FormatMP3},
		{"audio/pcm;rate=24000", voice.FormatMP3, voice.FormatPCM},
		{"audio/ogg", voice.FormatMP3, voice.FormatMP3},
		{"", voice.FormatPCM, voice.FormatPCM},
	}
	for _, tc := range cases {
		if got := formatFromMediaType(tc.header, tc.fallback); got != tc.want {
			t.Errorf("formatFromMediaType(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestDecodeTranscript_A13(t *testing.T) {
	if _, err := decodeTranscript([]byte(`not json`), "m"); err == nil {
		t.Fatal("decodeTranscript(malformed) = nil, want an error")
	}
	if _, err := decodeTranscript([]byte(`{"text":"hi","usage":{"cost":-1}}`), "m"); err == nil {
		t.Fatal("decodeTranscript(negative cost) = nil, want an error")
	}
	got, err := decodeTranscript([]byte(`{"text":"hi","usage":{"total_tokens":5}}`), "m")
	if err != nil {
		t.Fatalf("decodeTranscript: %v", err)
	}
	if got.Model != "m" || got.Usage.TotalTokens != 5 || got.Usage.CostSource != "provider" {
		t.Errorf("transcript = %+v", got)
	}
}

func TestUsageOf_A13(t *testing.T) {
	usage, err := usageOf(&usageWire{InputTokens: 3, OutputTokens: 2})
	if err != nil {
		t.Fatalf("usageOf: %v", err)
	}
	if usage.PromptTokens != 3 || usage.CompletionTokens != 2 || usage.TotalTokens != 5 {
		t.Errorf("usage = %+v, want total derived from the parts", usage)
	}
	if usage.CostSource != "provider" {
		t.Errorf("CostSource = %q, want provider", usage.CostSource)
	}
}

func TestStatusError_A05(t *testing.T) {
	const key = "sk-secret-value-123"
	resp := &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"2"}}}
	err := statusError(resp, []byte(`{"error":{"message":"slow down `+key+`"}}`), key)
	if err.Status != 429 || !err.Retryable || err.RetryAfter.Seconds() != 2 {
		t.Errorf("error = %+v", err)
	}
	if strings.Contains(err.Message, key) || strings.Contains(err.Body, key) {
		t.Error("the API key leaked")
	}
	if !strings.Contains(err.Message, "slow down") {
		t.Errorf("message = %q", err.Message)
	}
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Error("statusError did not return a *provider.Error")
	}
}
