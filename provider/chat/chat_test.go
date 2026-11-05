package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/route"
	"github.com/AutumnsGrove/nova/testkit"
)

// TestChat_ONE01_OneShotThroughRouter checks a one-shot completion runs
// through the same router a loop turn would, with no agent involved: the
// catalog id routes to Go, the wire carries the bare slug, and content,
// tool calls and usage come back (#59).
func TestChat_ONE01_OneShotThroughRouter(t *testing.T) {
	goSrv := testkit.NewGoServer(t, testkit.GoConfig{RequireSession: true},
		testkit.ServerTurn{Turn: testkit.Turn{
			Text:      "a title",
			ToolCalls: []testkit.Call{{ID: "c1", Name: "save", Args: `{"t":"x"}`}},
			Usage: &provider.Usage{PromptTokens: 32, CompletionTokens: 8, TotalTokens: 40,
				CostUSD: 0.00000576, CostSource: "allowance", AllowanceUSD: 0.00000576,
				ListCostUSD: 0.0000096, BillingSource: "go"},
		}})
	orSrv := testkit.NewServer(t)
	r := route.New(route.Config{
		Providers: []route.ProviderDecl{
			{Name: "openrouter", Kind: route.OpenRouter, BaseURL: orSrv.URL, APIKeyFunc: keyFunc("k-or")},
			{Name: "opencode-go", Kind: route.OpenCodeGo, BaseURL: goSrv.URL, APIKeyFunc: keyFunc("k-go")},
		},
		Routes: map[string]route.Route{
			"opencode-go/deepseek-v4.1-flash": {
				Attempts: []route.Attempt{{Provider: "opencode-go", Model: "deepseek-v4.1-flash"}},
			},
		},
		DefaultProvider: "openrouter",
		SessionID:       func() string { return "aux-1" },
	})
	resp, err := Complete(context.Background(), r, Request{
		Model:     "opencode-go/deepseek-v4.1-flash",
		Messages:  []message.Message{message.User("title this")},
		MaxTokens: 32,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Message.Content != "a title" {
		t.Errorf("content = %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Function.Name != "save" {
		t.Errorf("tool calls = %+v, want the save call", resp.Message.ToolCalls)
	}
	if resp.Usage.AllowanceUSD != 0.00000576 || resp.Provider != "opencode-go" {
		t.Errorf("usage/provider = %+v/%q, want the Go allowance numbers", resp.Usage, resp.Provider)
	}
	goReqs := goSrv.Requests()
	if len(goReqs) != 1 {
		t.Fatalf("Go saw %d requests, want 1", len(goReqs))
	}
	if !strings.Contains(string(goReqs[0].Body), `"model":"deepseek-v4.1-flash"`) {
		t.Errorf("wire body = %s, want the bare slug, not the catalog id", goReqs[0].Body)
	}
	if len(orSrv.Requests()) != 0 {
		t.Error("OpenRouter was called for a Go-routed one-shot")
	}
}

func keyFunc(key string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return key, nil }
}
