package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// stream runs one request against p and fails the test on error.
func stream(t *testing.T, p provider.Provider, req provider.Request) {
	t.Helper()
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
}

// userRequest is the smallest valid request for the preset.
func userRequest() provider.Request {
	return provider.Request{Model: "test/model", Messages: []message.Message{message.User("hi")}}
}

// TestNew_ORT01_NameAndBaseURL checks the preset is named and points at
// OpenRouter by default.
func TestNew_ORT01_NameAndBaseURL(t *testing.T) {
	p := New(Config{APIKey: "k"})
	if got := p.Name(); got != "openrouter" {
		t.Errorf("Name() = %q, want openrouter", got)
	}
	if got := p.BaseURL(); got != DefaultBaseURL {
		t.Errorf("BaseURL() = %q, want %q", got, DefaultBaseURL)
	}
}

// TestNew_ORT02_AttributionHeaders checks both attribution headers are sent.
func TestNew_ORT02_AttributionHeaders(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	stream(t, New(Config{APIKey: "k", BaseURL: srv.URL}), userRequest())

	header := srv.Requests()[0].Header
	if got := header.Get("HTTP-Referer"); got != DefaultReferer {
		t.Errorf("HTTP-Referer = %q, want %q", got, DefaultReferer)
	}
	if got := header.Get("X-Title"); got != DefaultTitle {
		t.Errorf("X-Title = %q, want %q", got, DefaultTitle)
	}
}

// TestNew_ORT03_CustomRefererTitle checks a Config can override the defaults.
func TestNew_ORT03_CustomRefererTitle(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	stream(t, New(Config{APIKey: "k", BaseURL: srv.URL, Referer: "https://example.test", Title: "Custom"}), userRequest())

	header := srv.Requests()[0].Header
	if got := header.Get("HTTP-Referer"); got != "https://example.test" {
		t.Errorf("HTTP-Referer = %q", got)
	}
	if got := header.Get("X-Title"); got != "Custom" {
		t.Errorf("X-Title = %q", got)
	}
}

// TestNew_ORT04_KeyFuncPerRequest checks APIKeyFunc supplies a fresh key for
// every request, so a refreshed credential takes effect mid-run.
func TestNew_ORT04_KeyFuncPerRequest(t *testing.T) {
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Turn: testkit.Turn{Text: "one"}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "two"}},
	)
	var n atomic.Int32
	p := New(Config{
		BaseURL: srv.URL,
		APIKeyFunc: func(context.Context) (string, error) {
			return fmt.Sprintf("key-%d", n.Add(1)), nil
		},
	})
	stream(t, p, userRequest())
	stream(t, p, userRequest())

	for i, want := range []string{"Bearer key-1", "Bearer key-2"} {
		if got := srv.Requests()[i].Header.Get("Authorization"); got != want {
			t.Errorf("request %d Authorization = %q, want %q", i, got, want)
		}
	}
}

// TestNew_ORT10_RoutingOnTheWire checks Routing.Options() reaches the request
// body unchanged through Request.ProviderOptions.
func TestNew_ORT10_RoutingOnTheWire(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	req := userRequest()
	req.ProviderOptions = Routing{Order: []string{"a", "b"}, DataCollection: "deny"}.Options()
	stream(t, New(Config{APIKey: "k", BaseURL: srv.URL}), req)

	var body map[string]any
	if err := json.Unmarshal(srv.Requests()[0].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	providerObj, ok := body["provider"].(map[string]any)
	if !ok {
		t.Fatalf("body has no provider object: %s", srv.Requests()[0].Body)
	}
	if providerObj["data_collection"] != "deny" {
		t.Errorf("provider.data_collection = %#v", providerObj["data_collection"])
	}
	order, ok := providerObj["order"].([]any)
	if !ok || len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Errorf("provider.order = %#v, want [a b]", providerObj["order"])
	}
}
