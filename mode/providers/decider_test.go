package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/testkit"
)

func deciderRequest(model string) decide.Request {
	return decide.Request{
		Model: model,
		State: "the evidence",
		Questions: map[string]decide.Question{
			"q": {Instructions: "pick", Criteria: map[string]string{"a": "one", "b": "two"}},
		},
	}
}

func TestProviders_DeciderResolvesAConfiguredRole(t *testing.T) {
	srv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{
		Body: `{"answers":{"q":{"choice":"a","probabilities":{"a":0.8,"b":0.2},"confidence":0.8}},"usage":{"input_tokens":3,"output_tokens":1,"cost":0.0001},"model":"typesafe/jev-1.14-20261014"}`,
	})
	cfg := config.Defaults()
	cfg.Provider.OpenRouter.BaseURL = srv.URL + "/api/v1"
	cfg.Decide.Roles = map[string]config.DecideRole{
		"oracle": {Model: "typesafe/jev-1.14", TimeoutMS: 2500},
	}
	d := BuildDecider(Options{Config: &cfg, Getenv: envFrom(map[string]string{auth.EnvAPIKey: "key"})})

	resp, err := d.Decide(context.Background(), deciderRequest("oracle"))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Model != "typesafe/jev-1.14-20261014" || resp.Answers["q"].Choice != "a" {
		t.Fatalf("response = %+v", resp)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(reqs))
	}
	if reqs[0].Path != "/api/v1/systemone" {
		t.Fatalf("path = %q, want the System One surface", reqs[0].Path)
	}
	if !strings.Contains(string(reqs[0].Body), `"model":"typesafe/jev-1.14"`) {
		t.Fatalf("body = %s, want the role's pinned model", reqs[0].Body)
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer key" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestProviders_DeciderRefusesAnAlias(t *testing.T) {
	cfg := config.Defaults()
	cfg.Decide.Roles = map[string]config.DecideRole{
		"oracle": {Model: "~typesafe/jev-latest"},
	}
	d := BuildDecider(Options{Config: &cfg, Getenv: envFrom(nil)})
	_, err := d.Decide(context.Background(), deciderRequest("oracle"))
	if err == nil || !strings.Contains(err.Error(), "alias") || !strings.Contains(err.Error(), "oracle") {
		t.Fatalf("error = %v, want an alias error naming the role", err)
	}
}
