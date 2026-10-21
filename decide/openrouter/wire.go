package openrouter

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/provider"
)

// requestWire is the body both surfaces accept. The state is carried as the
// caller gave it: a string stays a string, and a []decide.Source stays an array
// of {source, text} objects so sources never bleed together.
type requestWire struct {
	Model     string                  `json:"model"`
	State     any                     `json:"state"`
	Questions map[string]questionWire `json:"questions"`
}

// questionWire is one typed question. Criteria's keys are the option set for a
// choice question.
type questionWire struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// encodeRequest turns a decide.Request into the wire body.
func encodeRequest(req decide.Request) ([]byte, error) {
	questions := make(map[string]questionWire, len(req.Questions))
	for name, q := range req.Questions {
		kind := q.Kind
		if kind == "" {
			kind = decide.KindChoice
		}
		questions[name] = questionWire{Type: kind, Instructions: q.Instructions, Criteria: q.Criteria}
	}
	return json.Marshal(requestWire{Model: req.Model, State: req.State, Questions: questions})
}

// responseWire is the answer shape shared by the Decisions API and System One.
// Usage is a pointer so an omitted usage block is distinguishable from a
// genuine zero, which is a free call.
type responseWire struct {
	Answers map[string]answerWire `json:"answers"`
	Usage   *usageWire            `json:"usage"`
	Model   string                `json:"model,omitempty"`
}

type answerWire struct {
	Type          string             `json:"type,omitempty"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence"`
}

type usageWire struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

// decodeResponse parses and validates an answer body. The body is untrusted, so
// a malformed answer is an error, never a value passed to the caller.
func decodeResponse(req decide.Request, data []byte) (*decide.Response, error) {
	var wire responseWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("decide: parsing response: %w", err)
	}
	if wire.Usage != nil && (wire.Usage.InputTokens < 0 || wire.Usage.OutputTokens < 0 || wire.Usage.Cost < 0) {
		return nil, errors.New("decide: response has negative usage")
	}
	usage := provider.Usage{}
	if wire.Usage != nil {
		usage = provider.Usage{
			PromptTokens:     wire.Usage.InputTokens,
			CompletionTokens: wire.Usage.OutputTokens,
			TotalTokens:      wire.Usage.InputTokens + wire.Usage.OutputTokens,
			CostUSD:          wire.Usage.Cost,
			// The response reports an exact cost, so a zero is a real free
			// call, not a missing catalog price.
			CostSource: "provider",
		}
	}
	out := &decide.Response{
		Answers: make(map[string]decide.Answer, len(wire.Answers)),
		Usage:   usage,
		Model:   wire.Model,
	}
	for name, ans := range wire.Answers {
		out.Answers[name] = decide.Answer{
			Choice:        ans.Choice,
			Probabilities: ans.Probabilities,
			Confidence:    ans.Confidence,
		}
	}
	if err := decide.ValidateResponse(req, out); err != nil {
		return nil, err
	}
	return out, nil
}
