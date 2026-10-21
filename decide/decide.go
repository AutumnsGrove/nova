// Package decide defines the contract for decision models: a request carries a
// shared state and a set of typed questions, evaluated in parallel and in
// isolation, and returns typed answers with calibrated probabilities. It is the
// decision-model counterpart of provider.Provider for chat.
//
// The wire implementation lives in decide/openrouter. A caller may also supply
// its own Decider; Roles is the decorator that maps an application role such as
// "oracle" to a model id, a timeout, and an optional fallback.
package decide

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
)

// KindChoice is the question kind every decision model supports today: pick one
// option from Criteria, with a probability for each. Other kinds are carried
// through unchanged so a new wire kind does not need an API change here.
const KindChoice = "choice"

// Decider is a decision backend. Implementations must honor ctx cancellation
// and must return a non-nil error on failure.
type Decider interface {
	Decide(ctx context.Context, req Request) (*Response, error)
}

// Source is one entry of a multi-source State, for a comparison where each
// source must stay separate on the wire instead of being concatenated.
type Source struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}

// Request is one decision call. Every question is evaluated in parallel and in
// isolation against the same State, so a caller should fan out questions in one
// request rather than making one call per question.
type Request struct {
	// Model is a catalog id or a configured role name. Roles resolves a role;
	// a direct id must contain a slash so an unknown role is never mistaken for
	// a model id.
	Model string
	// State is the shared evidence: a string, or []Source for a multi-source
	// comparison.
	State any
	// Questions are keyed by the caller's name for the answer. The map must not
	// be empty.
	Questions map[string]Question
}

// Question is one typed question. Kind defaults to KindChoice when empty.
// Criteria maps each option to a short description of when it applies; for a
// choice question, its keys are the option set.
type Question struct {
	Kind         string            `json:"kind,omitempty"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// Answer is one question's result. A choice answer names one option from the
// question's Criteria and gives a probability for the options.
type Answer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence"`
}

// Response is one decision call's result.
type Response struct {
	// Answers is keyed by the request's question name.
	Answers map[string]Answer
	// Usage is the call's token and cost accounting. The Decisions API reports
	// one cost per call, not per question.
	Usage provider.Usage
	// Model is the build that actually answered, when the server reports it.
	// An alias moves silently, so a caller that records a benchmark result
	// should record this too.
	Model string
}

// Validate checks a request before it reaches the wire, so a malformed question
// is a local error instead of a 400 from the provider.
func (r Request) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return errors.New("decide: model is required")
	}
	if r.State == nil {
		return errors.New("decide: state is required")
	}
	if len(r.Questions) == 0 {
		return errors.New("decide: at least one question is required")
	}
	for name, q := range r.Questions {
		if name == "" {
			return errors.New("decide: question name is required")
		}
		if questionKind(q) == KindChoice && len(q.Criteria) == 0 {
			return fmt.Errorf("decide: question %q is a choice with no criteria", name)
		}
	}
	return nil
}

// ValidateResponse checks a response against the request that produced it. It
// rejects a choice outside the question's option set, a probability for an
// unknown option, a missing or unexpected answer, and a confidence outside
// [0,1]. The wire client treats a failure as a malformed response and does not
// pass it through to the caller.
func ValidateResponse(req Request, resp *Response) error {
	if resp == nil {
		return errors.New("decide: nil response")
	}
	for name, q := range req.Questions {
		ans, ok := resp.Answers[name]
		if !ok {
			return fmt.Errorf("decide: response has no answer for question %q", name)
		}
		if questionKind(q) != KindChoice {
			// A future kind's payload is not ours to validate yet.
			continue
		}
		if ans.Choice == "" {
			return fmt.Errorf("decide: answer %q has no choice", name)
		}
		if _, ok := q.Criteria[ans.Choice]; !ok {
			return fmt.Errorf("decide: answer %q chose %q, which is not one of its criteria", name, ans.Choice)
		}
		for option := range ans.Probabilities {
			if _, ok := q.Criteria[option]; !ok {
				return fmt.Errorf("decide: answer %q gives a probability for %q, which is not one of its criteria", name, option)
			}
		}
		if ans.Confidence < 0 || ans.Confidence > 1 {
			return fmt.Errorf("decide: answer %q has confidence %v outside [0,1]", name, ans.Confidence)
		}
	}
	for name := range resp.Answers {
		if _, ok := req.Questions[name]; !ok {
			return fmt.Errorf("decide: response answers question %q, which was not asked", name)
		}
	}
	return nil
}

// questionKind returns the effective kind, defaulting an empty kind to choice.
func questionKind(q Question) string {
	if q.Kind == "" {
		return KindChoice
	}
	return q.Kind
}
