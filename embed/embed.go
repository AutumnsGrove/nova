// Package embed defines the contract for embeddings: text in, vectors out, with
// the cost and provenance of the call.
//
// It is deliberately not a provider.Provider method. Embeddings have no
// messages, no streaming and no tools, and a request carries a batch instead of
// a conversation, so the shape that fits is the one decide uses: a small
// contract, one wire adapter per dialect, and a role table over the shared
// roles resolver. There is no vector store here; the caller owns its own data
// plane.
package embed

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
)

const (
	// MaxBatch is the largest number of inputs one provider request carries. An
	// adapter chunks to this, so no caller can trip a per-request limit.
	MaxBatch = 128
	// MaxInputChars caps one input before it leaves the process. It bounds the
	// request size and the time one pathological input can spend in a batch;
	// the model's own window may be shorter.
	MaxInputChars = 100_000
)

// Embedder is an embedding backend. Implementations must honor ctx
// cancellation and must return a non-nil error on failure. Vectors are in input
// order, and one call either succeeds completely or returns no vectors.
type Embedder interface {
	Embed(ctx context.Context, req Request) (*Response, error)
}

// Catalog reads a live embeddings roster. embed/openrouter's Client implements
// it, so a caller can list models without naming a vendor.
type Catalog interface {
	List(ctx context.Context) ([]provider.Model, error)
}

// Request is one embedding call. Input may carry up to MaxBatch texts for one
// wire request; an adapter chunks a larger slice and reassembles it.
type Request struct {
	// Model is a catalog id or a configured role name. A role is resolved by
	// Roles; a direct id must contain a slash.
	Model string `json:"model"`
	// Input is the text batch. Every entry must be non-empty.
	Input []string `json:"input"`
	// Truncate embeds a prefix of an over-long input instead of refusing it.
	// False is the default: a silently truncated embedding is a wrong vector,
	// not a cheap one.
	Truncate bool `json:"truncate,omitempty"`
}

// Response is one embedding call's result.
type Response struct {
	// Vectors is in request input order, and each vector is Dims long.
	Vectors [][]float32 `json:"vectors"`
	// Model is the configured id, not the provider's echo. A provider may
	// answer with a shorter slug, and recording the echo would make a stable
	// index look like a model change.
	Model string `json:"model"`
	// Dims is the number of components actually returned.
	Dims int `json:"dims"`
	// Usage is the call's token and cost accounting. There are no completion
	// tokens.
	Usage provider.Usage `json:"usage"`
}

// Validate checks a request before it reaches the wire, so a malformed input is
// a local error instead of a 400 from the provider.
func (r Request) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return errors.New("embed: model is required")
	}
	if len(r.Input) == 0 {
		return errors.New("embed: at least one input is required")
	}
	for i, text := range r.Input {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("embed: input %d is empty", i)
		}
		if !r.Truncate && len([]rune(text)) > MaxInputChars {
			return fmt.Errorf("embed: input %d is %d characters, over the %d cap; set truncate to embed a prefix", i, len([]rune(text)), MaxInputChars)
		}
	}
	return nil
}

// ValidateResponse checks a response against the request that produced it. It
// rejects a count mismatch, an empty vector, inconsistent dimensions, a Dims
// field that disagrees with the vectors, and a non-finite component. The wire
// adapter treats a failure as a malformed response and does not pass it to the
// caller.
func ValidateResponse(req Request, resp *Response) error {
	if resp == nil {
		return errors.New("embed: nil response")
	}
	if len(resp.Vectors) != len(req.Input) {
		return fmt.Errorf("embed: response has %d vectors for %d inputs", len(resp.Vectors), len(req.Input))
	}
	dims := resp.Dims
	for i, vector := range resp.Vectors {
		if len(vector) == 0 {
			return fmt.Errorf("embed: response vector %d is empty", i)
		}
		if i == 0 {
			if dims != len(vector) {
				return fmt.Errorf("embed: response dims is %d but vector 0 has %d components", dims, len(vector))
			}
			continue
		}
		if len(vector) != dims {
			return fmt.Errorf("embed: response vector %d has %d components, want %d", i, len(vector), dims)
		}
	}
	for i, vector := range resp.Vectors {
		for j, v := range vector {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return fmt.Errorf("embed: response vector %d component %d is not finite", i, j)
			}
		}
	}
	return nil
}

// TruncateText cuts a text to MaxInputChars runes without splitting one. An
// adapter calls it for a request whose Truncate is set.
func TruncateText(text string) string {
	if len(text) <= MaxInputChars { // bytes >= runes, so this is a cheap fast path
		return text
	}
	r := []rune(text)
	if len(r) <= MaxInputChars {
		return text
	}
	return string(r[:MaxInputChars])
}
