package openai

import (
	"encoding/json"
	"fmt"

	"github.com/AutumnsGrove/nova/provider"
)

// requestWire is the body an OpenAI-compatible embeddings endpoint accepts.
// EncodingFormat is pinned to "float" rather than left to the server default:
// the response type is `number[] | string` (base64), and a silent switch to
// base64 would decode as a confusing unmarshal error instead of a clear one.
type requestWire struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format"`
}

// responseWire is the response shape. Usage is a pointer so an omitted block is
// distinguishable from a genuine zero, which is a free call.
type responseWire struct {
	Data  []datumWire `json:"data"`
	Usage *usageWire  `json:"usage"`
}

// datumWire is one vector. Index is a pointer because it is documented as
// optional in some dialects; ignoring its absence would make a reordered batch
// attach every vector to the wrong text, so this adapter refuses it instead.
type datumWire struct {
	Index     *int      `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type usageWire struct {
	PromptTokens int      `json:"prompt_tokens"`
	TotalTokens  int      `json:"total_tokens"`
	Cost         *float64 `json:"cost"`
}

// decodeChunk parses one response body for n inputs. It remaps vectors by the
// response's index and rejects a missing, duplicate, or out-of-range index, so
// a reordered batch is never trusted by position. Every index must appear
// exactly once.
func decodeChunk(data []byte, n int) ([][]float32, provider.Usage, bool, error) {
	var wire responseWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, provider.Usage{}, false, fmt.Errorf("embed: parsing response: %w", err)
	}
	if wire.Usage != nil {
		if wire.Usage.PromptTokens < 0 || wire.Usage.TotalTokens < 0 || (wire.Usage.Cost != nil && *wire.Usage.Cost < 0) {
			return nil, provider.Usage{}, false, fmt.Errorf("embed: response has negative usage")
		}
	}
	vectors := make([][]float32, n)
	seen := make([]bool, n)
	for _, d := range wire.Data {
		if d.Index == nil {
			return nil, provider.Usage{}, false, fmt.Errorf("embed: response has a vector with no index")
		}
		index := *d.Index
		if index < 0 || index >= n {
			return nil, provider.Usage{}, false, fmt.Errorf("embed: response index %d is outside the %d inputs", index, n)
		}
		if seen[index] {
			return nil, provider.Usage{}, false, fmt.Errorf("embed: response repeats index %d", index)
		}
		if len(d.Embedding) == 0 {
			return nil, provider.Usage{}, false, fmt.Errorf("embed: response vector %d is empty", index)
		}
		seen[index] = true
		vectors[index] = d.Embedding
	}
	for index, ok := range seen {
		if !ok {
			return nil, provider.Usage{}, false, fmt.Errorf("embed: response has no vector for index %d", index)
		}
	}
	return vectors, usageOf(wire.Usage), wire.Usage != nil, nil
}

// usageOf maps the wire block to the shared usage type. A reported block makes
// the cost provider-reported even when it is zero, which is a real free call.
func usageOf(wire *usageWire) provider.Usage {
	if wire == nil {
		return provider.Usage{}
	}
	usage := provider.Usage{
		PromptTokens: wire.PromptTokens,
		TotalTokens:  wire.TotalTokens,
		CostSource:   "provider",
	}
	if wire.TotalTokens == 0 {
		usage.TotalTokens = wire.PromptTokens
	}
	if wire.Cost != nil {
		usage.CostUSD = *wire.Cost
	}
	return usage
}
