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
	"strconv"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
)

// The question kinds the Decisions API documents. KindChoice is the default
// when Question.Kind is empty. An unknown kind is carried through unchanged, so
// a new wire kind does not need an API change here.
const (
	// KindChoice picks one option from Criteria.
	KindChoice = "choice"
	// KindNoul reports the probability that a yes/no condition holds.
	KindNoul = "noul"
	// KindScore places the state on an ordered set of Levels.
	KindScore = "score"
)

// Decider is a decision backend. Implementations must honor ctx cancellation
// and must return a non-nil error on failure.
type Decider interface {
	Decide(ctx context.Context, req Request) (*Response, error)
}

// Catalog reads a live decision-model roster. decide/openrouter's Client
// implements it, so a benchmark can accept a catalog without naming a vendor.
type Catalog interface {
	List(ctx context.Context) ([]provider.Model, error)
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
	Model string `json:"model"`
	// State is the shared evidence: a string, or []Source for a multi-source
	// comparison.
	State any `json:"state"`
	// Questions are keyed by the caller's name for the answer. The map must not
	// be empty.
	Questions map[string]Question `json:"questions"`
}

// Question is one typed question. Kind defaults to KindChoice when empty.
//
// A choice question uses Criteria, whose keys are the option set. A noul
// question may use Criteria with exactly the keys "true" and "false". A score
// question uses Levels, ordered lowest first; its wire criteria are that list.
// Instructions may be a string, or an object/array when the model accepts
// structured instructions.
type Question struct {
	Kind         string            `json:"kind,omitempty"`
	Instructions any               `json:"instructions,omitempty"`
	Criteria     map[string]string `json:"criteria,omitempty"`
	Levels       []string          `json:"levels,omitempty"`
}

// Answer is one question's result. The fields that matter depend on the
// question kind: a choice answer names one option from Criteria, a noul answer
// is a probability, and a score answer is a position on Levels with an optional
// legend and per-level probabilities keyed by their index.
type Answer struct {
	Kind          string             `json:"kind,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Noul          float64            `json:"noul"`
	Score         float64            `json:"score"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence"`
}

// Response is one decision call's result.
type Response struct {
	// Answers is keyed by the request's question name.
	Answers map[string]Answer `json:"answers"`
	// Usage is the call's token and cost accounting. The Decisions API reports
	// one cost per call, not per question.
	Usage provider.Usage `json:"usage"`
	// Model is the build that actually answered, when the server reports it.
	// An alias moves silently, so a caller that records a benchmark result
	// should record this too.
	Model string `json:"model,omitempty"`
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
		if err := validateQuestion(name, q); err != nil {
			return err
		}
	}
	return nil
}

// validateQuestion checks the field the kind actually uses.
func validateQuestion(name string, q Question) error {
	switch questionKind(q) {
	case KindChoice:
		if len(q.Criteria) == 0 {
			return fmt.Errorf("decide: question %q is a choice with no criteria", name)
		}
		if len(q.Levels) > 0 {
			return fmt.Errorf("decide: question %q is a choice with levels; a choice uses criteria", name)
		}
	case KindNoul:
		if len(q.Criteria) > 0 && !validNoulCriteria(q.Criteria) {
			return fmt.Errorf("decide: question %q is a noul whose criteria must be exactly true and false", name)
		}
		if len(q.Levels) > 0 {
			return fmt.Errorf("decide: question %q is a noul with levels; a noul uses optional true/false criteria", name)
		}
	case KindScore:
		if len(q.Levels) == 0 {
			return fmt.Errorf("decide: question %q is a score with no levels", name)
		}
		if len(q.Criteria) > 0 {
			return fmt.Errorf("decide: question %q is a score with criteria; a score uses levels", name)
		}
	}
	return nil
}

// validNoulCriteria reports the exact map a noul question accepts when it
// supplies descriptions.
func validNoulCriteria(c map[string]string) bool {
	_, yes := c["true"]
	_, no := c["false"]
	return len(c) == 2 && yes && no
}

// ValidateResponse checks a response against the request that produced it. It
// rejects a value outside the question's option set or level range, a missing or
// unexpected answer, and a confidence or probability outside [0,1]. The wire
// client treats a failure as a malformed response and does not pass it through
// to the caller.
func ValidateResponse(req Request, resp *Response) error {
	if resp == nil {
		return errors.New("decide: nil response")
	}
	for name, q := range req.Questions {
		ans, ok := resp.Answers[name]
		if !ok {
			return fmt.Errorf("decide: response has no answer for question %q", name)
		}
		kind := questionKind(q)
		if ans.Kind != "" && ans.Kind != kind {
			return fmt.Errorf("decide: answer %q is a %q answer for a %q question", name, ans.Kind, kind)
		}
		switch kind {
		case KindChoice:
			if err := validateChoiceAnswer(name, q, ans); err != nil {
				return err
			}
		case KindNoul:
			if ans.Noul < 0 || ans.Noul > 1 {
				return fmt.Errorf("decide: answer %q has noul %v outside [0,1]", name, ans.Noul)
			}
		case KindScore:
			if err := validateScoreAnswer(name, q, ans); err != nil {
				return err
			}
		}
	}
	for name := range resp.Answers {
		if _, ok := req.Questions[name]; !ok {
			return fmt.Errorf("decide: response answers question %q, which was not asked", name)
		}
	}
	return nil
}

// validateChoiceAnswer checks that the choice and its probabilities are inside
// the question's option set.
func validateChoiceAnswer(name string, q Question, ans Answer) error {
	if ans.Choice == "" {
		return fmt.Errorf("decide: answer %q has no choice", name)
	}
	if _, ok := q.Criteria[ans.Choice]; !ok {
		return fmt.Errorf("decide: answer %q chose %q, which is not one of its criteria", name, ans.Choice)
	}
	for option, p := range ans.Probabilities {
		if _, ok := q.Criteria[option]; !ok {
			return fmt.Errorf("decide: answer %q gives a probability for %q, which is not one of its criteria", name, option)
		}
		if p < 0 || p > 1 {
			return fmt.Errorf("decide: answer %q gives probability %v for %q, outside [0,1]", name, p, option)
		}
	}
	if ans.Confidence < 0 || ans.Confidence > 1 {
		return fmt.Errorf("decide: answer %q has confidence %v outside [0,1]", name, ans.Confidence)
	}
	return nil
}

// validateScoreAnswer checks the score against the ordered levels and checks
// that a legend or probability map only names real levels.
func validateScoreAnswer(name string, q Question, ans Answer) error {
	maxScore := float64(len(q.Levels) - 1)
	if ans.Score < 0 || ans.Score > maxScore {
		return fmt.Errorf("decide: answer %q has score %v outside [0,%v]", name, ans.Score, maxScore)
	}
	for index, level := range ans.Legend {
		i, err := strconv.Atoi(index)
		if err != nil || i < 0 || i >= len(q.Levels) {
			return fmt.Errorf("decide: answer %q has a legend index %q outside its levels", name, index)
		}
		if level != q.Levels[i] {
			return fmt.Errorf("decide: answer %q legend %q is %q, want %q", name, index, level, q.Levels[i])
		}
	}
	for index, p := range ans.Probabilities {
		i, err := strconv.Atoi(index)
		if err != nil || i < 0 || i >= len(q.Levels) {
			return fmt.Errorf("decide: answer %q has a probability index %q outside its levels", name, index)
		}
		if p < 0 || p > 1 {
			return fmt.Errorf("decide: answer %q gives probability %v at %q, outside [0,1]", name, p, index)
		}
	}
	if ans.Confidence < 0 || ans.Confidence > 1 {
		return fmt.Errorf("decide: answer %q has confidence %v outside [0,1]", name, ans.Confidence)
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
