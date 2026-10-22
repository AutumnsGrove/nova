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

// questionWire is one typed question. Criteria is an option map for choice and
// noul, and an ordered level list for score, so it is carried as any.
type questionWire struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}

// encodeRequest turns a decide.Request into the wire body.
func encodeRequest(req decide.Request) ([]byte, error) {
	questions := make(map[string]questionWire, len(req.Questions))
	for name, q := range req.Questions {
		kind := q.Kind
		if kind == "" {
			kind = decide.KindChoice
		}
		questions[name] = questionWire{
			Type:         kind,
			Instructions: q.Instructions,
			Criteria:     wireCriteria(q, kind),
		}
	}
	return json.Marshal(requestWire{Model: req.Model, State: req.State, Questions: questions})
}

// wireCriteria picks the field the kind sends: a level list for score, and the
// option map otherwise. A caller that uses the wrong field for its kind is
// caught by Request.Validate before this runs.
func wireCriteria(q decide.Question, kind string) any {
	if kind == decide.KindScore {
		if len(q.Levels) == 0 {
			return nil
		}
		return q.Levels
	}
	if len(q.Criteria) > 0 {
		return q.Criteria
	}
	if len(q.Levels) > 0 {
		return q.Levels
	}
	return nil
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
	Choice        string             `json:"choice,omitempty"`
	Noul          float64            `json:"noul,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
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
			Kind:          ans.Type,
			Choice:        ans.Choice,
			Noul:          ans.Noul,
			Score:         ans.Score,
			Legend:        ans.Legend,
			Probabilities: ans.Probabilities,
			Confidence:    ans.Confidence,
		}
	}
	if err := decide.ValidateResponse(req, out); err != nil {
		return nil, err
	}
	return out, nil
}
