package mode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
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

// fixedDecider runs fn for each Decide call.
type fixedDecider struct {
	fn func(context.Context, decide.Request) (*decide.Response, error)
}

func (f fixedDecider) Decide(ctx context.Context, req decide.Request) (*decide.Response, error) {
	return f.fn(ctx, req)
}

func validDecisionCommand() proto.Command {
	return proto.Command{
		Type: proto.CmdDecide,
		Decision: &decide.Request{
			Model: "vendor/model",
			State: "the evidence",
			Questions: map[string]decide.Question{
				"q": {Kind: decide.KindNoul, Instructions: "is it?"},
			},
		},
	}
}

func TestDecide_ProtocolCommand(t *testing.T) {
	s, _ := testServer(t)
	s.decider = testkit.NewDecider(nil, testkit.DecisionTurn{
		Model:   "build-1",
		Answers: map[string]decide.Answer{"q": {Kind: decide.KindNoul, Noul: 0.9}},
		Usage:   provider.Usage{PromptTokens: 4, CostUSD: 0.01},
	})
	resp := s.Handle(context.Background(), validDecisionCommand())
	if !resp.OK {
		t.Fatalf("decide = %+v", resp)
	}
	var got decide.Response
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("decode: %v (data=%s)", err, resp.Data)
	}
	if got.Model != "build-1" || got.Answers["q"].Noul != 0.9 || got.Usage.PromptTokens != 4 {
		t.Fatalf("response = %+v", got)
	}
}

func TestDecide_ProtocolCommandRejectsABadRequest(t *testing.T) {
	s, _ := testServer(t)
	called := false
	s.decider = fixedDecider{fn: func(context.Context, decide.Request) (*decide.Response, error) {
		called = true
		return &decide.Response{}, nil
	}}

	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdDecide})
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("missing request: %+v", resp)
	}
	cmd := validDecisionCommand()
	cmd.Decision.Questions["q"] = decide.Question{Kind: decide.KindChoice}
	resp = s.Handle(context.Background(), cmd)
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("invalid question: %+v", resp)
	}
	if called {
		t.Error("decider was called for an invalid request")
	}
}

func TestDecide_ProtocolCommandMapsErrors(t *testing.T) {
	s, _ := testServer(t)
	s.decider = fixedDecider{fn: func(context.Context, decide.Request) (*decide.Response, error) {
		return nil, &provider.Error{Status: 500, Message: "provider down"}
	}}
	resp := s.Handle(context.Background(), validDecisionCommand())
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeProviderError {
		t.Fatalf("provider error: %+v", resp)
	}

	s.decider = fixedDecider{fn: func(context.Context, decide.Request) (*decide.Response, error) {
		return nil, errors.New(`unknown role "oracle"`)
	}}
	resp = s.Handle(context.Background(), validDecisionCommand())
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("configuration error: %+v", resp)
	}
}

// fakeCatalog plays a live decision roster.
type fakeCatalog struct {
	models []provider.Model
	err    error
}

func (f fakeCatalog) List(context.Context) ([]provider.Model, error) { return f.models, f.err }

// commandText decodes the text of a run_command response.
func commandText(t *testing.T, resp proto.Response) string {
	t.Helper()
	if !resp.OK {
		t.Fatalf("command = %+v", resp)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		t.Fatalf("decode: %v (data=%s)", err, resp.Data)
	}
	return out.Text
}

func TestDecide_SlashListsRoles(t *testing.T) {
	s, _ := testServer(t)
	s.cfg.Decide.Roles = map[string]config.DecideRole{
		"verify": {Model: "vendor/verify"},
		"oracle": {Model: "typesafe/jev-1.14", Fallback: "typesafe/jev-1.13", TimeoutMS: 2500},
	}
	text := commandText(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "decide"}))
	for _, want := range []string{"oracle -> typesafe/jev-1.14", "fallback typesafe/jev-1.13", "[2500ms]", "verify -> vendor/verify"} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, want it to contain %q", text, want)
		}
	}
	if strings.Index(text, "oracle") > strings.Index(text, "verify") {
		t.Errorf("roles are not sorted: %q", text)
	}
}

func TestDecide_SlashListsLiveModels(t *testing.T) {
	s, _ := testServer(t)
	s.catalog = fakeCatalog{models: []provider.Model{
		{ID: "typesafe/jev-1.14", Name: "Jev 1.14", ContextWindow: 262144},
		{ID: "openai/gpt-6-luna-decisions"},
	}}
	text := commandText(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "decide", Args: "models"}))
	want := []string{"typesafe/jev-1.14", "Jev 1.14", "262144 tokens", "openai/gpt-6-luna-decisions"}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("text = %q, want it to contain %q", text, w)
		}
	}
}

func TestDecide_SlashRejectsUnknownArguments(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "decide", Args: "ask"})
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("response = %+v", resp)
	}
}
