package decide

import (
	"strings"
	"testing"
)

func validRequest() Request {
	return Request{
		Model: "typesafe/jev-1.14",
		State: "the evidence",
		Questions: map[string]Question{
			"triage": {Instructions: "which queue", Criteria: map[string]string{"a": "urgent", "b": "normal"}},
		},
	}
}

func TestRequestValidate_AcceptsAChoiceQuestion(t *testing.T) {
	if err := validRequest().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRequestValidate_DefaultsAnEmptyKindToChoice(t *testing.T) {
	req := validRequest()
	req.Questions["triage"] = Question{Instructions: "which queue"}
	if err := req.Validate(); err == nil || !strings.Contains(err.Error(), "criteria") {
		t.Fatalf("Validate error = %v, want a missing-criteria error", err)
	}
}

func TestRequestValidate_RejectsMalformedRequests(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Request)
		want string
	}{
		{"no model", func(r *Request) { r.Model = " " }, "model"},
		{"no state", func(r *Request) { r.State = nil }, "state"},
		{"no questions", func(r *Request) { r.Questions = nil }, "question"},
		{"empty question name", func(r *Request) { r.Questions[""] = Question{Kind: KindChoice, Criteria: map[string]string{"a": "b"}} }, "name"},
		{"choice with no criteria", func(r *Request) { r.Questions["triage"] = Question{Kind: KindChoice} }, "criteria"},
	}
	for _, tc := range cases {
		req := validRequest()
		tc.edit(&req)
		err := req.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to name %q", tc.name, err, tc.want)
		}
	}
}

func TestRequestValidate_AllowsAnUnknownKindWithoutChoiceCriteria(t *testing.T) {
	req := validRequest()
	req.Questions["rank"] = Question{Kind: "rank", Instructions: "how good"}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRequestValidate_NoulCriteria(t *testing.T) {
	req := validRequest()

	// Noul criteria are optional.
	req.Questions["q"] = Question{Kind: KindNoul, Instructions: "is it?"}
	if err := req.Validate(); err != nil {
		t.Fatalf("noul without criteria: %v", err)
	}

	req.Questions["q"] = Question{Kind: KindNoul, Criteria: map[string]string{"true": "yes", "false": "no"}}
	if err := req.Validate(); err != nil {
		t.Fatalf("noul with true/false criteria: %v", err)
	}

	for _, criteria := range []map[string]string{
		{"true": "yes"},
		{"true": "yes", "false": "no", "maybe": "perhaps"},
		{"yes": "yes", "no": "no"},
	} {
		req.Questions["q"] = Question{Kind: KindNoul, Criteria: criteria}
		if err := req.Validate(); err == nil {
			t.Errorf("noul criteria %v was accepted, want an error", criteria)
		}
	}

	req.Questions["q"] = Question{Kind: KindNoul, Levels: []string{"low", "high"}}
	if err := req.Validate(); err == nil {
		t.Error("noul with levels was accepted, want an error")
	}
}

func TestRequestValidate_ScoreLevels(t *testing.T) {
	req := validRequest()

	req.Questions["q"] = Question{Kind: KindScore, Instructions: "how urgent?"}
	if err := req.Validate(); err == nil || !strings.Contains(err.Error(), "levels") {
		t.Fatalf("score without levels: %v, want a levels error", err)
	}

	req.Questions["q"] = Question{Kind: KindScore, Levels: []string{"low", "mid", "high"}}
	if err := req.Validate(); err != nil {
		t.Fatalf("score with levels: %v", err)
	}

	req.Questions["q"] = Question{Kind: KindScore, Levels: []string{"low"}, Criteria: map[string]string{"a": "b"}}
	if err := req.Validate(); err == nil || !strings.Contains(err.Error(), "criteria") {
		t.Fatalf("score with criteria: %v, want a criteria error", err)
	}
}

func TestValidateResponse_AcceptsAValidAnswer(t *testing.T) {
	resp := &Response{Answers: map[string]Answer{
		"triage": {Choice: "a", Probabilities: map[string]float64{"a": 0.8, "b": 0.2}, Confidence: 0.8},
	}}
	if err := ValidateResponse(validRequest(), resp); err != nil {
		t.Fatalf("ValidateResponse: %v", err)
	}
}

func TestValidateResponse_RejectsAMalformedAnswer(t *testing.T) {
	cases := []struct {
		name string
		resp *Response
		want string
	}{
		{"nil response", nil, "nil"},
		{"missing answer", &Response{}, "no answer"},
		{"extra answer", &Response{Answers: map[string]Answer{
			"triage": {Choice: "a", Confidence: 0.8},
			"other":  {Choice: "a"},
		}}, "not asked"},
		{"empty choice", &Response{Answers: map[string]Answer{"triage": {Confidence: 0.8}}}, "no choice"},
		{"choice outside criteria", &Response{Answers: map[string]Answer{"triage": {Choice: "z", Confidence: 0.8}}}, "not one of its criteria"},
		{"probability outside criteria", &Response{Answers: map[string]Answer{
			"triage": {Choice: "a", Probabilities: map[string]float64{"a": 0.9, "z": 0.1}, Confidence: 0.9},
		}}, "probability"},
		{"probability outside zero to one", &Response{Answers: map[string]Answer{
			"triage": {Choice: "a", Probabilities: map[string]float64{"a": 1.2, "b": -0.2}, Confidence: 0.9},
		}}, "outside [0,1]"},
		{"confidence above one", &Response{Answers: map[string]Answer{"triage": {Choice: "a", Confidence: 1.1}}}, "confidence"},
		{"confidence below zero", &Response{Answers: map[string]Answer{"triage": {Choice: "a", Confidence: -0.1}}}, "confidence"},
	}
	for _, tc := range cases {
		err := ValidateResponse(validRequest(), tc.resp)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to name %q", tc.name, err, tc.want)
		}
	}
}

func TestValidateResponse_SkipsAKindItDoesNotKnow(t *testing.T) {
	req := Request{Model: "m/x", State: "s", Questions: map[string]Question{
		"rank": {Kind: "rank"},
	}}
	resp := &Response{Answers: map[string]Answer{"rank": {}}}
	if err := ValidateResponse(req, resp); err != nil {
		t.Fatalf("ValidateResponse: %v", err)
	}
}

func TestValidateResponse_Noul(t *testing.T) {
	req := Request{Model: "m/x", State: "s", Questions: map[string]Question{
		"q": {Kind: KindNoul},
	}}
	resp := &Response{Answers: map[string]Answer{"q": {Kind: KindNoul, Noul: 0.96}}}
	if err := ValidateResponse(req, resp); err != nil {
		t.Fatalf("ValidateResponse: %v", err)
	}

	for _, noul := range []float64{-0.1, 1.1} {
		resp.Answers["q"] = Answer{Kind: KindNoul, Noul: noul}
		if err := ValidateResponse(req, resp); err == nil || !strings.Contains(err.Error(), "noul") {
			t.Errorf("noul %v: error = %v, want an out-of-range error", noul, err)
		}
	}

	resp.Answers["q"] = Answer{Kind: KindChoice, Noul: 0.5}
	if err := ValidateResponse(req, resp); err == nil || !strings.Contains(err.Error(), "answer") {
		t.Errorf("wrong kind: error = %v, want a kind-mismatch error", err)
	}
}

func scoreRequest() Request {
	return Request{Model: "m/x", State: "s", Questions: map[string]Question{
		"q": {Kind: KindScore, Levels: []string{"low", "mid", "high"}},
	}}
}

func TestValidateResponse_Score(t *testing.T) {
	valid := Answer{
		Kind:          KindScore,
		Score:         1.99,
		Legend:        map[string]string{"0": "low", "1": "mid", "2": "high"},
		Probabilities: map[string]float64{"0": 0, "1": 0.01, "2": 0.99},
		Confidence:    0.99,
	}
	resp := &Response{Answers: map[string]Answer{"q": valid}}
	if err := ValidateResponse(scoreRequest(), resp); err != nil {
		t.Fatalf("ValidateResponse: %v", err)
	}

	cases := []struct {
		name string
		ans  Answer
		want string
	}{
		{"score above the levels", Answer{Kind: KindScore, Score: 2.1}, "score"},
		{"score below zero", Answer{Kind: KindScore, Score: -0.1}, "score"},
		{"legend index outside", Answer{Kind: KindScore, Legend: map[string]string{"3": "high"}}, "legend index"},
		{"legend value does not match", Answer{Kind: KindScore, Legend: map[string]string{"0": "LOW"}}, "legend"},
		{"probability index outside", Answer{Kind: KindScore, Probabilities: map[string]float64{"3": 0.5}}, "probability index"},
		{"probability outside zero to one", Answer{Kind: KindScore, Probabilities: map[string]float64{"0": 1.5}}, "probability"},
		{"confidence outside zero to one", Answer{Kind: KindScore, Confidence: 1.1}, "confidence"},
	}
	for _, tc := range cases {
		resp := &Response{Answers: map[string]Answer{"q": tc.ans}}
		err := ValidateResponse(scoreRequest(), resp)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to name %q", tc.name, err, tc.want)
		}
	}
}
