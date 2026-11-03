package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/embed"
	"github.com/AutumnsGrove/nova/embed/openai"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

const testKey = "sk-secret-value-123"

// newClient builds a client pointed at one scripted server.
func newClient(t *testing.T, turns ...testkit.EmbeddingServerTurn) (*openai.Client, *testkit.EmbeddingServer) {
	t.Helper()
	srv := testkit.NewEmbeddingServer(t, turns...)
	return openai.New(openai.Config{BaseURL: srv.URL, APIKey: testKey}), srv
}

// responseBody renders vectors in input order.
func responseBody(t testing.TB, vectors [][]float32) string {
	t.Helper()
	data := make([]map[string]any, len(vectors))
	for i, v := range vectors {
		data[i] = map[string]any{"index": i, "embedding": v}
	}
	body, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(body)
}

// responseBodyWithUsage renders vectors plus a usage block.
func responseBodyWithUsage(t testing.TB, vectors [][]float32, promptTokens int, cost float64) string {
	t.Helper()
	data := make([]map[string]any, len(vectors))
	for i, v := range vectors {
		data[i] = map[string]any{"index": i, "embedding": v}
	}
	body, err := json.Marshal(map[string]any{
		"data":  data,
		"usage": map[string]any{"prompt_tokens": promptTokens, "cost": cost},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(body)
}

// TestEmbed_EMB01_OneBodyCarriesTheBatchAndPinsTheEncoding asserts the wire
// bytes, not the response.
func TestEmbed_EMB01_OneBodyCarriesTheBatchAndPinsTheEncoding(t *testing.T) {
	client, srv := newClient(t, testkit.EmbeddingServerTurn{Body: responseBody(t, [][]float32{{1, 2}, {3, 4}})})
	if _, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{"a", "b"}}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want one", len(reqs))
	}
	if reqs[0].Path != "/embeddings" {
		t.Errorf("path = %q, want /embeddings", reqs[0].Path)
	}
	var got struct {
		Model          string   `json:"model"`
		Input          []string `json:"input"`
		EncodingFormat string   `json:"encoding_format"`
	}
	if err := json.Unmarshal(reqs[0].Body, &got); err != nil {
		t.Fatalf("body: %v", err)
	}
	if got.Model != "vendor/embed" || got.EncodingFormat != "float" || len(got.Input) != 2 || got.Input[0] != "a" || got.Input[1] != "b" {
		t.Fatalf("body = %+v, want model, [a b], float", got)
	}
	if auth := reqs[0].Header.Get("Authorization"); auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want the bearer key", auth)
	}
}

// TestEmbed_EMB02_MapsByIndexAndRefusesBadIndexes covers reorder, duplicate,
// out-of-range and missing.
func TestEmbed_EMB02_MapsByIndexAndRefusesBadIndexes(t *testing.T) {
	client, _ := newClient(t, testkit.EmbeddingServerTurn{
		Body: `{"data":[{"index":1,"embedding":[30,40]},{"index":0,"embedding":[10,20]}]}`,
	})
	resp, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if resp.Vectors[0][0] != 10 || resp.Vectors[1][0] != 30 {
		t.Fatalf("vectors = %v, want index 0 first", resp.Vectors)
	}

	for name, body := range map[string]string{
		"duplicate":    `{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]}]}`,
		"out of range": `{"data":[{"index":5,"embedding":[1]},{"index":1,"embedding":[2]}]}`,
		"missing":      `{"data":[{"index":0,"embedding":[1]}]}`,
		"no index":     `{"data":[{"embedding":[1]},{"index":1,"embedding":[2]}]}`,
	} {
		c, _ := newClient(t, testkit.EmbeddingServerTurn{Body: body})
		if _, err := c.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{"a", "b"}}); err == nil {
			t.Errorf("%s: Embed succeeded, want an error", name)
		}
	}
}

// TestEmbed_EMB03_ModelIsTheConfiguredID keeps a stable index from looking like
// a model change when the provider echoes a shorter slug.
func TestEmbed_EMB03_ModelIsTheConfiguredID(t *testing.T) {
	client, _ := newClient(t, testkit.EmbeddingServerTurn{Body: `{"model":"pplx-embed-v1-0.6b","data":[{"index":0,"embedding":[1,2,3]}]}`})
	resp, err := client.Embed(context.Background(), embed.Request{Model: "perplexity/pplx-embed-v1-0.6b", Input: []string{"a"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if resp.Model != "perplexity/pplx-embed-v1-0.6b" {
		t.Fatalf("Model = %q, want the configured id", resp.Model)
	}
}

// TestEmbed_EMB04_DimsIsWhatCameBack pins the component count.
func TestEmbed_EMB04_DimsIsWhatCameBack(t *testing.T) {
	client, _ := newClient(t, testkit.EmbeddingServerTurn{Body: responseBody(t, [][]float32{{1, 2, 3}, {4, 5, 6}})})
	resp, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if resp.Dims != 3 || len(resp.Vectors[1]) != 3 {
		t.Fatalf("Dims = %d, vector len = %d, want 3", resp.Dims, len(resp.Vectors[1]))
	}
}

// TestEmbed_EMB05_ChunksAndReassemblesInOrder drives 300 inputs through three
// requests and checks the order survives.
func TestEmbed_EMB05_ChunksAndReassemblesInOrder(t *testing.T) {
	input := make([]string, 300)
	for i := range input {
		input[i] = "text"
	}
	var turns []testkit.EmbeddingServerTurn
	start := 0
	for _, n := range []int{128, 128, 44} {
		vectors := make([][]float32, n)
		for i := range vectors {
			vectors[i] = []float32{float32(start + i), 1}
		}
		turns = append(turns, testkit.EmbeddingServerTurn{Body: responseBody(t, vectors)})
		start += n
	}
	client, srv := newClient(t, turns...)
	resp, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: input})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Vectors) != 300 {
		t.Fatalf("vectors = %d, want 300", len(resp.Vectors))
	}
	for i, v := range resp.Vectors {
		if v[0] != float32(i) {
			t.Fatalf("vector %d = %v, want input order", i, v)
		}
	}
	reqs := srv.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	for i, want := range []int{128, 128, 44} {
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.Unmarshal(reqs[i].Body, &body); err != nil {
			t.Fatalf("body %d: %v", i, err)
		}
		if len(body.Input) != want {
			t.Errorf("request %d carried %d inputs, want %d", i, len(body.Input), want)
		}
	}
}

// TestEmbed_EMB05_OneFailedChunkDiscardsTheWholeCall proves no partial vectors
// come back.
func TestEmbed_EMB05_OneFailedChunkDiscardsTheWholeCall(t *testing.T) {
	input := make([]string, 129)
	for i := range input {
		input[i] = "text"
	}
	first := make([][]float32, 128)
	for i := range first {
		first[i] = []float32{1, 2}
	}
	client, _ := newClient(t,
		testkit.EmbeddingServerTurn{Body: responseBody(t, first)},
		testkit.EmbeddingServerTurn{Status: 500, Body: `{"error":{"message":"boom"}}`},
	)
	resp, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: input})
	if err == nil {
		t.Fatal("Embed succeeded, want the failed chunk's error")
	}
	if resp != nil {
		t.Fatalf("response = %+v, want nil vectors", resp.Vectors)
	}
}

// TestEmbed_EMB06_InconsistentDimensionsAreErrors covers within one response
// and across chunks.
func TestEmbed_EMB06_InconsistentDimensionsAreErrors(t *testing.T) {
	client, _ := newClient(t, testkit.EmbeddingServerTurn{
		Body: `{"data":[{"index":0,"embedding":[1,2,3]},{"index":1,"embedding":[4,5]}]}`,
	})
	if _, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{"a", "b"}}); err == nil {
		t.Error("within a response: Embed succeeded, want an error")
	}

	input := make([]string, 129)
	for i := range input {
		input[i] = "text"
	}
	first := make([][]float32, 128)
	for i := range first {
		first[i] = []float32{1, 2}
	}
	client, _ = newClient(t,
		testkit.EmbeddingServerTurn{Body: responseBody(t, first)},
		testkit.EmbeddingServerTurn{Body: responseBody(t, [][]float32{{1, 2, 3}})},
	)
	if _, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: input}); err == nil {
		t.Error("across chunks: Embed succeeded, want an error")
	}
}

// TestEmbed_EMB07_UsageIsProviderReported maps tokens and cost, and tolerates a
// response with none.
func TestEmbed_EMB07_UsageIsProviderReported(t *testing.T) {
	client, _ := newClient(t, testkit.EmbeddingServerTurn{Body: responseBodyWithUsage(t, [][]float32{{1, 2}}, 12, 0.0003)})
	resp, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{"a"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if resp.Usage.PromptTokens != 12 || resp.Usage.CostUSD != 0.0003 || resp.Usage.CostSource != "provider" {
		t.Fatalf("usage = %+v, want 12 prompt tokens and provider cost", resp.Usage)
	}
	if resp.Usage.CompletionTokens != 0 {
		t.Errorf("completion tokens = %d, want none", resp.Usage.CompletionTokens)
	}

	client, _ = newClient(t, testkit.EmbeddingServerTurn{Body: responseBody(t, [][]float32{{1, 2}})})
	resp, err = client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{"a"}})
	if err != nil {
		t.Fatalf("Embed without usage: %v", err)
	}
	if resp.Usage != (provider.Usage{}) {
		t.Fatalf("usage = %+v, want zero", resp.Usage)
	}
}

// TestEmbed_EMB08_HTTPFailureIsAScrubbedProviderError checks the type, the
// retryability, the bounded body, and that the key never appears.
func TestEmbed_EMB08_HTTPFailureIsAScrubbedProviderError(t *testing.T) {
	client, _ := newClient(t, testkit.EmbeddingServerTurn{
		Status:  429,
		Headers: map[string]string{"Retry-After": "2"},
		Body:    `{"error":{"message":"bad key ` + testKey + ` ` + strings.Repeat("x", 4000) + `"}}`,
	})
	_, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{"a"}})
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want *provider.Error", err)
	}
	if pe.Status != 429 || !pe.Retryable || pe.RetryAfter.Seconds() != 2 {
		t.Fatalf("error = %+v, want a retryable 429 with Retry-After", pe)
	}
	if len(pe.Body) > 8<<10 {
		t.Errorf("body is %d bytes, want it bounded", len(pe.Body))
	}
	for _, s := range []string{pe.Message, pe.Body, pe.Error()} {
		if strings.Contains(s, testKey) {
			t.Fatalf("secret leaked in %q", s)
		}
	}
}

// TestEmbed_EMB10_TruncateIsAnExplicitOptIn covers the local refusal and the
// prefix that goes on the wire.
func TestEmbed_EMB10_TruncateIsAnExplicitOptIn(t *testing.T) {
	long := strings.Repeat("a", embed.MaxInputChars+1)
	client, srv := newClient(t, testkit.EmbeddingServerTurn{Body: responseBody(t, [][]float32{{1}})})
	if _, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{long}}); err == nil {
		t.Fatal("Embed succeeded, want a local bad-request error")
	}
	if got := len(srv.Requests()); got != 0 {
		t.Fatalf("requests = %d, want none for a refused input", got)
	}

	client, srv = newClient(t, testkit.EmbeddingServerTurn{Body: responseBody(t, [][]float32{{1}})})
	if _, err := client.Embed(context.Background(), embed.Request{Model: "vendor/embed", Input: []string{long}, Truncate: true}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want one", len(reqs))
	}
	var body struct {
		Input []string `json:"input"`
	}
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if got := len([]rune(body.Input[0])); got != embed.MaxInputChars {
		t.Fatalf("input is %d runes, want the %d-rune prefix", got, embed.MaxInputChars)
	}
}
