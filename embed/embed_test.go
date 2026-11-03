package embed_test

import (
	"math"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/embed"
)

func TestRequest_ValidateRefusesWhatCannotBeEmbedded(t *testing.T) {
	long := strings.Repeat("a", embed.MaxInputChars+1)
	cases := map[string]embed.Request{
		"no model":     {Input: []string{"a"}},
		"no input":     {Model: "vendor/embed"},
		"empty input":  {Model: "vendor/embed", Input: []string{"a", "  "}},
		"over the cap": {Model: "vendor/embed", Input: []string{long}},
	}
	for name, req := range cases {
		if err := req.Validate(); err == nil {
			t.Errorf("%s: Validate succeeded, want an error", name)
		}
	}
}

func TestRequest_ValidateAcceptsTruncatingAnOverLongInput(t *testing.T) {
	req := embed.Request{Model: "vendor/embed", Input: []string{strings.Repeat("a", embed.MaxInputChars+1)}, Truncate: true}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateResponse(t *testing.T) {
	req := embed.Request{Model: "vendor/embed", Input: []string{"a", "b"}}
	good := &embed.Response{Vectors: [][]float32{{1, 2}, {3, 4}}, Model: "vendor/embed", Dims: 2}
	if err := embed.ValidateResponse(req, good); err != nil {
		t.Fatalf("ValidateResponse: %v", err)
	}

	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	bad := map[string]*embed.Response{
		"nil":            nil,
		"count mismatch": {Vectors: [][]float32{{1, 2}}, Dims: 2},
		"empty vector":   {Vectors: [][]float32{{}, {1}}, Dims: 1},
		"short vector":   {Vectors: [][]float32{{1, 2}, {3}}, Dims: 2},
		"dims disagree":  {Vectors: [][]float32{{1, 2}, {3, 4}}, Dims: 3},
		"nan component":  {Vectors: [][]float32{{1, nan}, {3, 4}}, Dims: 2},
		"infinite value": {Vectors: [][]float32{{1, inf}, {3, 4}}, Dims: 2},
	}
	for name, resp := range bad {
		if err := embed.ValidateResponse(req, resp); err == nil {
			t.Errorf("%s: ValidateResponse succeeded, want an error", name)
		}
	}
}

func TestTruncateTextKeepsThePrefixAndTheRunes(t *testing.T) {
	text := strings.Repeat("é", embed.MaxInputChars+1)
	got := embed.TruncateText(text)
	if len([]rune(got)) != embed.MaxInputChars {
		t.Fatalf("got %d runes, want %d", len([]rune(got)), embed.MaxInputChars)
	}
	if !strings.HasPrefix(text, got) {
		t.Fatal("truncated text is not a prefix of the input")
	}
}
