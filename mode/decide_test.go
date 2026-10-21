package mode

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

func TestDecide_ServerWiresTheRoleAndTheToolSeam(t *testing.T) {
	t.Setenv(auth.EnvAPIKey, "test-key")
	decisionSrv := testkit.NewDecisionServer(t, testkit.DecisionServerTurn{
		Body: `{"answers":{"q":{"choice":"a","probabilities":{"a":0.8,"b":0.2},"confidence":0.8}},"usage":{"input_tokens":3,"output_tokens":1,"cost":0.0001},"model":"typesafe/jev-1.14-20261014"}`,
	})
	providerSrv := testkit.NewProvider(testkit.RealClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "triage_ask", Args: `{}`}}},
		testkit.Turn{Text: "done"},
	)

	var got *decide.Response
	var decideErr error
	triage := tool.New(tool.Info{
		Name:        "triage_ask",
		Description: "ask the decision model",
		Parameters:  map[string]any{"type": "object"},
		ReadOnly:    true,
	}, func(ctx context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		if tctx.Decide == nil {
			return tool.Errorf("no decide seam"), nil
		}
		resp, err := tctx.Decide(ctx, decide.Request{
			Model: "oracle",
			State: "the evidence",
			Questions: map[string]decide.Question{
				"q": {Instructions: "pick", Criteria: map[string]string{"a": "one", "b": "two"}},
			},
		})
		if err != nil {
			decideErr = err
			return tool.Errorf("decide: %v", err), nil
		}
		got = resp
		tctx.AddUsage(resp.Usage)
		return tool.Text("chose %s", resp.Answers["q"].Choice), nil
	})

	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	cfg.Provider.OpenRouter.BaseURL = decisionSrv.URL + "/api/v1"
	cfg.Decide.Roles = map[string]config.DecideRole{
		"oracle": {Model: "typesafe/jev-1.14", TimeoutMS: 2500},
	}
	s, err := New(Options{
		Config:     &cfg,
		CWD:        t.TempDir(),
		Home:       t.TempDir(),
		Provider:   providerSrv,
		Session:    filepath.Join(t.TempDir(), "session.jsonl"),
		Version:    "test",
		Tools:      []string{"triage_ask"},
		ExtraTools: []tool.Tool{triage},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var out, errOut bytes.Buffer
	if code := RunPrint(context.Background(), s, "go", &out, &errOut); code != 0 {
		t.Fatalf("RunPrint exit = %d (stderr: %s)", code, errOut.String())
	}
	if got == nil || got.Answers["q"].Choice != "a" {
		t.Fatalf("tool got %+v (decide err: %v); decision requests: %+v", got, decideErr, decisionSrv.Requests())
	}
	reqs := decisionSrv.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/api/v1/systemone" {
		t.Fatalf("decision requests = %+v, want one on the System One surface", reqs)
	}
	if !strings.Contains(string(reqs[0].Body), `"model":"typesafe/jev-1.14"`) {
		t.Fatalf("decision body = %s, want the resolved role model", reqs[0].Body)
	}

	// The tool's answer reached the model on the next turn.
	loopReqs := providerSrv.Requests()
	if len(loopReqs) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(loopReqs))
	}
	last := loopReqs[1].Messages[len(loopReqs[1].Messages)-1]
	if !strings.Contains(last.Content, "chose a") {
		t.Fatalf("last message = %q, want the tool result", last.Content)
	}
}
