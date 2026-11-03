package openrouter_test

import (
	"context"
	"testing"

	"github.com/AutumnsGrove/nova/embed/openrouter"
	"github.com/AutumnsGrove/nova/testkit"
)

// TestEmbed_EMB14_RosterDecodesTheEmbeddingsFilter checks the live roster shape:
// price per million, context length, the embedding modality, a malformed price
// that is unknown rather than fatal, and an entry with no id that is skipped.
func TestEmbed_EMB14_RosterDecodesTheEmbeddingsFilter(t *testing.T) {
	srv := testkit.NewEmbeddingServer(t, testkit.EmbeddingServerTurn{Body: `{"data":[
		{"id":"perplexity/pplx-embed-v1-0.6b","name":"PPLX Embed","context_length":32768,"pricing":{"prompt":"0.000004"},"architecture":{"output_modalities":["embeddings"]}},
		{"id":"","name":"nameless","pricing":{"prompt":"1"}},
		{"id":"local/free","pricing":{"prompt":"not-a-number"}}
	]}`})
	client := openrouter.New(openrouter.Config{BaseURL: srv.URL, APIKey: "sk-test-value-123"})
	models, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2 (the entry with no id is skipped)", len(models))
	}
	if m := models[0]; m.ID != "perplexity/pplx-embed-v1-0.6b" || m.ContextWindow != 32768 || m.InPerM != 4 || !m.SupportsEmbeddings() {
		t.Fatalf("entry = %+v, want the roster values", m)
	}
	if m := models[1]; m.InPerM != 0 || !m.SupportsEmbeddings() {
		t.Fatalf("entry = %+v, want an unknown price and the embedding modality", m)
	}
	if reqs := srv.Requests(); len(reqs) != 1 || reqs[0].Method != "GET" {
		t.Fatalf("requests = %+v, want one roster GET", reqs)
	}
}
