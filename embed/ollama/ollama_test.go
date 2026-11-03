package ollama_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AutumnsGrove/nova/embed"
	"github.com/AutumnsGrove/nova/embed/ollama"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// TestEmbed_EMB09_OllamaBatchIsLocalAndFree drives a batch through the local
// shape: one request, vectors in order, no cost and no token count.
func TestEmbed_EMB09_OllamaBatchIsLocalAndFree(t *testing.T) {
	srv := testkit.NewEmbeddingServer(t, testkit.EmbeddingServerTurn{
		Body: `{"embeddings":[[1,2,3],[4,5,6]]}`,
	})
	client := ollama.New(ollama.Config{BaseURL: srv.URL})
	resp, err := client.Embed(context.Background(), embed.Request{Model: "nomic-embed-text", Input: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if resp.Dims != 3 || len(resp.Vectors) != 2 || resp.Vectors[1][0] != 4 {
		t.Fatalf("response = %+v, want two 3-component vectors in order", resp)
	}
	if resp.Usage != (provider.Usage{}) {
		t.Fatalf("usage = %+v, want none for a local model", resp.Usage)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/api/embed" {
		t.Fatalf("requests = %+v, want one /api/embed", reqs)
	}
	var body struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Model != "nomic-embed-text" || len(body.Input) != 2 {
		t.Fatalf("body = %+v, want the model and both inputs", body)
	}
}
