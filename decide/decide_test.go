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
	req.Questions["score"] = Question{Kind: "score", Instructions: "how good"}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
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
		"score": {Kind: "score"},
	}}
	resp := &Response{Answers: map[string]Answer{"score": {}}}
	if err := ValidateResponse(req, resp); err != nil {
		t.Fatalf("ValidateResponse: %v", err)
	}
}
