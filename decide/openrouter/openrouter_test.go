package openrouter

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

func validRequest(model string) decide.Request {
	return decide.Request{
		Model: model,
		State: "the evidence",
		Questions: map[string]decide.Question{
			"triage": {Instructions: "pick a queue", Criteria: map[string]string{"a": "urgent", "b": "normal"}},
		},
	}
}

const validBody = `{"answers":{"triage":{"choice":"a","probabilities":{"a":0.8,"b":0.2},"confidence":0.8}},"usage":{"input_tokens":10,"output_tokens":2,"cost":0.0002},"model":"vendor/build"}`

func TestDecide_UsesDecisionsAPIForANonJevModel(t *testing.T) {
	srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{Body: validBody})
	c := New(Config{BaseURL: srv.URL + "/api/v1", APIKey: "key"})
	resp, err := c.Decide(context.Background(), validRequest("vendor/model"))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Answers["triage"].Choice != "a" || resp.Model != "vendor/build" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 2 || resp.Usage.TotalTokens != 12 {
		t.Errorf("Usage = %+v, want the wire tokens", resp.Usage)
	}
	if resp.Usage.CostUSD != 0.0002 || resp.Usage.CostSource != "provider" {
		t.Errorf("Usage = %+v, want the provider cost", resp.Usage)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/api/alpha/decisions" {
		t.Fatalf("paths = %+v, want the Decisions API", reqs)
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer key" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestDecide_UsesSystemOneForAJevModel(t *testing.T) {
	srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{Body: validBody})
	c := New(Config{BaseURL: srv.URL + "/api/v1"})
	if _, err := c.Decide(context.Background(), validRequest("typesafe/jev-1.14")); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/api/v1/systemone" {
		t.Fatalf("path = %+v, want the System One surface", reqs)
	}
}

func TestDecide_GoldenRequestBodies(t *testing.T) {
	cases := []struct {
		name string
		req  decide.Request
		want string
	}{
		{
			name: "string state",
			req:  validRequest("vendor/model"),
			want: `{"model":"vendor/model","state":"the evidence","questions":{"triage":{"type":"choice","instructions":"pick a queue","criteria":{"a":"urgent","b":"normal"}}}}`,
		},
		{
			name: "multi-source state",
			req: decide.Request{
				Model: "vendor/model",
				State: []decide.Source{{Source: "one", Text: "first"}, {Source: "two", Text: "second"}},
				Questions: map[string]decide.Question{
					"q": {Instructions: "pick", Criteria: map[string]string{"a": "one"}},
				},
			},
			want: `{"model":"vendor/model","state":[{"source":"one","text":"first"},{"source":"two","text":"second"}],"questions":{"q":{"type":"choice","instructions":"pick","criteria":{"a":"one"}}}}`,
		},
		{
			name: "empty kind defaults to choice",
			req: decide.Request{
				Model: "vendor/model",
				State: "s",
				Questions: map[string]decide.Question{
					"q": {Instructions: "pick", Criteria: map[string]string{"a": "one"}},
				},
			},
			want: `{"model":"vendor/model","state":"s","questions":{"q":{"type":"choice","instructions":"pick","criteria":{"a":"one"}}}}`,
		},
		{
			name: "unknown kind passes through",
			req: decide.Request{
				Model: "vendor/model",
				State: "s",
				Questions: map[string]decide.Question{
					"q": {Kind: "score", Instructions: "rate"},
				},
			},
			want: `{"model":"vendor/model","state":"s","questions":{"q":{"type":"score","instructions":"rate"}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{Body: `{"answers":{}}`})
			// The scripted body does not match the question set, so the call
			// fails validation; the request has already been recorded.
			_, _ = New(Config{BaseURL: srv.URL}).Decide(context.Background(), tc.req)
			reqs := srv.Requests()
			if len(reqs) != 1 {
				t.Fatalf("recorded %d requests, want 1", len(reqs))
			}
			if got := string(reqs[0].Body); got != tc.want {
				t.Fatalf("body =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestDecide_ValidatesBeforeTheWire(t *testing.T) {
	srv := testkit.NewDecisionServer(t)
	req := validRequest("vendor/model")
	req.Questions["triage"] = decide.Question{Kind: decide.KindChoice}
	if _, err := New(Config{BaseURL: srv.URL}).Decide(context.Background(), req); err == nil || !strings.Contains(err.Error(), "criteria") {
		t.Fatalf("error = %v, want a local criteria error", err)
	}
	if got := len(srv.Requests()); got != 0 {
		t.Fatalf("recorded %d requests, want 0", got)
	}
}

func TestDecide_MapsStatusErrors(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		headers   map[string]string
		retryable bool
		after     time.Duration
	}{
		{"bad request", http.StatusBadRequest, nil, false, 0},
		{"payment required", http.StatusPaymentRequired, nil, false, 0},
		{"rate limited", http.StatusTooManyRequests, map[string]string{"Retry-After": "2"}, true, 2 * time.Second},
		{"server error", http.StatusInternalServerError, nil, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{
				Status:  tc.status,
				Body:    `{"error":{"message":"denied"}}`,
				Headers: tc.headers,
			})
			_, err := New(Config{BaseURL: srv.URL}).Decide(context.Background(), validRequest("vendor/model"))
			var pe *provider.Error
			if !errors.As(err, &pe) {
				t.Fatalf("error = %v, want *provider.Error", err)
			}
			if pe.Status != tc.status || pe.Retryable != tc.retryable || pe.RetryAfter != tc.after {
				t.Fatalf("provider.Error = %+v, want status %d retryable %v after %v", pe, tc.status, tc.retryable, tc.after)
			}
			if !strings.Contains(pe.Message, "denied") {
				t.Errorf("Message = %q, want the server's message", pe.Message)
			}
		})
	}
}

func TestDecide_RejectsMalformedResponses(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"choice outside criteria", `{"answers":{"triage":{"choice":"z","confidence":0.8}},"usage":{"input_tokens":1,"output_tokens":1,"cost":0.1}}`, "criteria"},
		{"missing answer", `{"answers":{},"usage":{"input_tokens":1,"output_tokens":1}}`, "no answer"},
		{"truncated body", `{"answers":`, "parsing"},
		{"negative usage", `{"answers":{},"usage":{"input_tokens":-1}}`, "negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{Body: tc.body})
			_, err := New(Config{BaseURL: srv.URL}).Decide(context.Background(), validRequest("vendor/model"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestDecide_CancellationIsNotRetryable(t *testing.T) {
	srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{Hang: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(Config{BaseURL: srv.URL}).Decide(ctx, validRequest("vendor/model"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestDecide_DeadlineIsRetryable(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	_, err := New(Config{BaseURL: "http://127.0.0.1:1"}).Decide(ctx, validRequest("vendor/model"))
	var pe *provider.Error
	if !errors.As(err, &pe) || !pe.Retryable {
		t.Fatalf("error = %v, want a retryable provider error", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error does not unwrap to context.DeadlineExceeded: %v", err)
	}
}

func TestDecide_KeyStaysInTheHeader(t *testing.T) {
	srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{Status: http.StatusBadRequest, Body: `{"error":{"message":"denied"}}`})
	_, err := New(Config{BaseURL: srv.URL, APIKey: "super-secret"}).Decide(context.Background(), validRequest("vendor/model"))
	if err == nil {
		t.Fatal("Decide succeeded, want an error")
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error leaked the key: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "Bearer super-secret" {
		t.Fatalf("Authorization header = %+v", reqs)
	}
}

func TestDecide_APIKeyFuncWins(t *testing.T) {
	srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{Body: validBody})
	c := New(Config{
		BaseURL: srv.URL,
		APIKey:  "static",
		APIKeyFunc: func(context.Context) (string, error) {
			return "dynamic", nil
		},
	})
	if _, err := c.Decide(context.Background(), validRequest("vendor/model")); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got := srv.Requests()[0].Header.Get("Authorization"); got != "Bearer dynamic" {
		t.Fatalf("Authorization = %q, want the func's key", got)
	}
}

func TestDecide_NoKeySendsNoAuthorization(t *testing.T) {
	srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{Body: validBody})
	if _, err := New(Config{BaseURL: srv.URL}).Decide(context.Background(), validRequest("vendor/model")); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got := srv.Requests()[0].Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want none", got)
	}
}

func TestIsJev(t *testing.T) {
	cases := map[string]bool{
		"typesafe/jev-1.14":                true,
		"~typesafe/jev-latest":             true,
		"jev-1.13":                         true,
		"openai/gpt-6-decisions":           false,
		"perplexity/pplx-decider-v1.1-27b": false,
	}
	for id, want := range cases {
		if got := isJev(id); got != want {
			t.Errorf("isJev(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestDecisionsURL(t *testing.T) {
	cases := map[string]string{
		"https://openrouter.ai/api/v1": "https://openrouter.ai/api/alpha/decisions",
		"http://127.0.0.1:1234/api/v1": "http://127.0.0.1:1234/api/alpha/decisions",
		"http://127.0.0.1:1234":        "http://127.0.0.1:1234/api/alpha/decisions",
		"not-a-url":                    "not-a-url/api/alpha/decisions",
	}
	for base, want := range cases {
		if got := decisionsURL(base); got != want {
			t.Errorf("decisionsURL(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestList_ReadsAndMapsTheLiveRoster(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[
			{"id":"typesafe/jev-1.14","name":"Jev 1.14","context_length":262144,"pricing":{"prompt":"0.0000001","completion":"0.0000002"},"architecture":{"input_modalities":["text"],"output_modalities":["decisions"]}},
			{"id":"openai/gpt-6-luna-decisions","name":"Luna Decisions","context_length":1050000,"pricing":{"prompt":"bogus","completion":"0.000001"},"architecture":{}},
			{"name":"no id"}
		]}`)
	}))
	defer srv.Close()

	models, err := List(context.Background(), Config{BaseURL: srv.URL + "/api/v1", APIKey: "key"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotPath != "/api/v1/models" || gotQuery != "output_modalities=decisions" || gotAuth != "Bearer key" {
		t.Fatalf("request path=%q query=%q auth=%q", gotPath, gotQuery, gotAuth)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2 (the entry with no id is skipped)", len(models))
	}
	jev := models[0]
	if jev.ID != "typesafe/jev-1.14" || jev.Provider != ProviderName || jev.ContextWindow != 262144 {
		t.Errorf("first model = %+v", jev)
	}
	if math.Abs(jev.InPerM-0.1) > 1e-9 || math.Abs(jev.OutPerM-0.2) > 1e-9 {
		t.Errorf("prices = %v/%v, want 0.1/0.2", jev.InPerM, jev.OutPerM)
	}
	if !jev.SupportsDecisions() {
		t.Errorf("first model lost the decisions modality: %+v", jev)
	}
	if models[1].InPerM != 0 {
		t.Errorf("malformed price = %v, want 0", models[1].InPerM)
	}
	if !models[1].SupportsDecisions() {
		t.Errorf("entry with no architecture lost the decisions modality: %+v", models[1])
	}
}

func TestList_ErrorsAreNotAnEmptyRoster(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, `{"error":{"message":"down"}}`},
		{"unparseable", http.StatusOK, `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			models, err := List(context.Background(), Config{BaseURL: srv.URL})
			if err == nil {
				t.Fatalf("List = %v, want an error", models)
			}
		})
	}
}
