package proto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/embed"
)

// TestCommand_EMB15_EmbedRoundTrip pins the wire shape the embed command carries
// and the result a client decodes.
func TestCommand_EMB15_EmbedRoundTrip(t *testing.T) {
	cmd := Command{
		Type: CmdEmbed,
		ID:   "1",
		Embedding: &embed.Request{
			Model:    "index",
			Input:    []string{"a", "b"},
			Truncate: true,
		},
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`"type":"embed"`, `"embedding":{"model":"index"`, `"input":["a","b"]`, `"truncate":true`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("frame = %s, want it to contain %s", data, want)
		}
	}

	var got Command
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Embedding == nil || got.Embedding.Model != "index" || len(got.Embedding.Input) != 2 || !got.Embedding.Truncate {
		t.Fatalf("Embedding = %+v", got.Embedding)
	}

	resp, err := json.Marshal(embed.Response{
		Vectors: [][]float32{{1, 2}},
		Model:   "index",
		Dims:    2,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`"vectors":[[1,2]]`, `"dims":2`, `"model":"index"`} {
		if !strings.Contains(string(resp), want) {
			t.Errorf("response = %s, want it to contain %s", resp, want)
		}
	}
}
