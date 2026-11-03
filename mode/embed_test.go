package mode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/embed"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// fixedEmbedder runs fn for each Embed call.
type fixedEmbedder struct {
	fn func(context.Context, embed.Request) (*embed.Response, error)
}

func (f fixedEmbedder) Embed(ctx context.Context, req embed.Request) (*embed.Response, error) {
	return f.fn(ctx, req)
}

// fakeEmbedCatalog plays a live embeddings roster.
type fakeEmbedCatalog struct {
	models []provider.Model
	err    error
}

func (f fakeEmbedCatalog) List(context.Context) ([]provider.Model, error) { return f.models, f.err }

func validEmbedCommand() proto.Command {
	return proto.Command{
		Type:      proto.CmdEmbed,
		Embedding: &embed.Request{Model: "vendor/model", Input: []string{"a"}},
	}
}

// TestEmbed_EMB15_ProtocolCommand round-trips one request through the server.
func TestEmbed_EMB15_ProtocolCommand(t *testing.T) {
	s, _ := testServer(t)
	s.embedder = fixedEmbedder{fn: func(context.Context, embed.Request) (*embed.Response, error) {
		return &embed.Response{
			Vectors: [][]float32{{1, 2, 3}},
			Model:   "vendor/model",
			Dims:    3,
			Usage:   provider.Usage{PromptTokens: 4, CostUSD: 0.01, CostSource: "provider"},
		}, nil
	}}
	resp := s.Handle(context.Background(), validEmbedCommand())
	if !resp.OK {
		t.Fatalf("embed = %+v", resp)
	}
	var got embed.Response
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("decode: %v (data=%s)", err, resp.Data)
	}
	if got.Model != "vendor/model" || got.Dims != 3 || len(got.Vectors) != 1 || got.Usage.PromptTokens != 4 {
		t.Fatalf("response = %+v", got)
	}
}

// TestEmbed_EMB15_BadRequestChangesNothing refuses a missing or malformed
// request before the embedder runs.
func TestEmbed_EMB15_BadRequestChangesNothing(t *testing.T) {
	s, _ := testServer(t)
	called := false
	s.embedder = fixedEmbedder{fn: func(context.Context, embed.Request) (*embed.Response, error) {
		called = true
		return &embed.Response{}, nil
	}}
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdEmbed}); resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("missing request: %+v", resp)
	}
	cmd := validEmbedCommand()
	cmd.Embedding.Input = nil
	if resp := s.Handle(context.Background(), cmd); resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("empty input: %+v", resp)
	}
	if called {
		t.Error("embedder was called for an invalid request")
	}
}

// TestEmbed_EMB15_MapsErrors keeps a provider failure a provider_error and a
// configuration failure a bad_request.
func TestEmbed_EMB15_MapsErrors(t *testing.T) {
	s, _ := testServer(t)
	s.embedder = fixedEmbedder{fn: func(context.Context, embed.Request) (*embed.Response, error) {
		return nil, &provider.Error{Status: 500, Message: "provider down"}
	}}
	if resp := s.Handle(context.Background(), validEmbedCommand()); resp.OK || resp.Error.Code != proto.CodeProviderError {
		t.Fatalf("provider error: %+v", resp)
	}
	s.embedder = fixedEmbedder{fn: func(context.Context, embed.Request) (*embed.Response, error) {
		return nil, errors.New(`unknown role "index"`)
	}}
	if resp := s.Handle(context.Background(), validEmbedCommand()); resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("configuration error: %+v", resp)
	}
}

// TestEmbed_EMB15_ToolSeamWiresTheRole drives the whole path: config role, the
// built embedder, the tool seam, and the usage hook.
func TestEmbed_EMB15_ToolSeamWiresTheRole(t *testing.T) {
	t.Setenv(auth.EnvAPIKey, "test-key")
	embeddingSrv := testkit.NewEmbeddingServer(t, testkit.EmbeddingServerTurn{
		Body: `{"data":[{"index":0,"embedding":[1,2,3]}],"usage":{"prompt_tokens":5,"cost":0.0002}}`,
	})
	providerSrv := testkit.NewProvider(testkit.RealClock(),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "vectorize", Args: `{}`}}},
		testkit.Turn{Text: "done"},
	)

	var got *embed.Response
	vectorize := tool.New(tool.Info{
		Name:        "vectorize",
		Description: "embed text",
		Parameters:  map[string]any{"type": "object"},
		ReadOnly:    true,
	}, func(ctx context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		if tctx.Embed == nil {
			return tool.Errorf("no embed seam"), nil
		}
		resp, err := tctx.Embed(ctx, embed.Request{Model: "index", Input: []string{"hello"}})
		if err != nil {
			return tool.Errorf("embed: %v", err), nil
		}
		got = resp
		tctx.AddUsage(resp.Usage)
		return tool.Text("dims %d", resp.Dims), nil
	})

	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	cfg.Provider.OpenRouter.BaseURL = embeddingSrv.URL + "/api/v1"
	cfg.Embed.Roles = map[string]config.EmbedRole{
		"index": {Model: "perplexity/pplx-embed-v1-0.6b", Dims: 3},
	}
	s, err := New(Options{
		Config:     &cfg,
		CWD:        t.TempDir(),
		Home:       t.TempDir(),
		Provider:   providerSrv,
		Session:    filepath.Join(t.TempDir(), "session.jsonl"),
		Version:    "test",
		Tools:      []string{"vectorize"},
		ExtraTools: []tool.Tool{vectorize},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if code := RunPrint(context.Background(), s, "go", io.Discard, io.Discard); code != 0 {
		t.Fatalf("RunPrint exit = %d", code)
	}
	if got == nil || got.Dims != 3 || got.Usage.PromptTokens != 5 {
		t.Fatalf("tool got %+v; embedding requests: %+v", got, embeddingSrv.Requests())
	}
	reqs := embeddingSrv.Requests()
	if len(reqs) != 1 || !strings.HasSuffix(reqs[0].Path, "/embeddings") {
		t.Fatalf("embedding requests = %+v, want one /embeddings", reqs)
	}
	if !strings.Contains(string(reqs[0].Body), `"model":"perplexity/pplx-embed-v1-0.6b"`) {
		t.Fatalf("body = %s, want the resolved role model", reqs[0].Body)
	}
}

// TestEmbed_EMB15_SlashListsRoles renders the configured role table.
func TestEmbed_EMB15_SlashListsRoles(t *testing.T) {
	s, _ := testServer(t)
	s.cfg.Embed.Roles = map[string]config.EmbedRole{
		"search": {Model: "vendor/small", Dims: 768},
		"index":  {Model: "perplexity/pplx-embed-v1-0.6b", Fallback: "perplexity/pplx-embed-v1-4b", TimeoutMS: 2500, Dims: 1024},
	}
	text := commandText(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "embed"}))
	for _, want := range []string{"index -> perplexity/pplx-embed-v1-0.6b", "fallback perplexity/pplx-embed-v1-4b", "[1024 dims]", "[2500ms]", "search -> vendor/small"} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, want it to contain %q", text, want)
		}
	}
	if strings.Index(text, "index") > strings.Index(text, "search") {
		t.Errorf("roles are not sorted: %q", text)
	}
}

// TestEmbed_EMB15_SlashListsLiveModels renders the roster.
func TestEmbed_EMB15_SlashListsLiveModels(t *testing.T) {
	s, _ := testServer(t)
	s.embedCatalog = fakeEmbedCatalog{models: []provider.Model{
		{ID: "perplexity/pplx-embed-v1-0.6b", Name: "PPLX Embed", ContextWindow: 32768, InPerM: 4},
	}}
	text := commandText(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "embed", Args: "models"}))
	for _, want := range []string{"perplexity/pplx-embed-v1-0.6b", "PPLX Embed", "32768 tokens", "$4/M"} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, want it to contain %q", text, want)
		}
	}
}

// TestEmbed_EMB15_SlashRejectsUnknownArguments pins the usage error.
func TestEmbed_EMB15_SlashRejectsUnknownArguments(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "embed", Args: "ask"})
	if resp.OK || resp.Error == nil || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("response = %+v", resp)
	}
}
