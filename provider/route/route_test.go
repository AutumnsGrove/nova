package route

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

const (
	goID      = "opencode-go/deepseek-v4.1-flash"
	goSlug    = "deepseek-v4.1-flash"
	orSlug    = "deepseek/deepseek-v4.1-flash"
	orOnly    = "openrouter/some-model"
	session   = "thread-1"
	userAgent = "polaris-test/0"
)

// fixture is a Router over two testkit doubles: a session-requiring Go
// double and a plain OpenRouter-shaped server.
type fixture struct {
	router *Router
	goSrv  *testkit.Server
	orSrv  *testkit.Server
}

func newFixture(t *testing.T, goTurns []testkit.ServerTurn, orTurns []testkit.ServerTurn, mutate func(*Config)) *fixture {
	t.Helper()
	goSrv := testkit.NewGoServer(t, testkit.GoConfig{RequireSession: true}, goTurns...)
	orSrv := testkit.NewServer(t, orTurns...)
	cfg := Config{
		Providers: []ProviderDecl{
			{Name: "openrouter", Kind: OpenRouter, BaseURL: orSrv.URL, APIKeyFunc: keyFunc("k-or")},
			{Name: "opencode-go", Kind: OpenCodeGo, BaseURL: goSrv.URL, APIKeyFunc: keyFunc("k-go")},
		},
		Routes: map[string]Route{
			goID: {
				Attempts:   []Attempt{{Provider: "opencode-go", Model: goSlug}, {Provider: "openrouter", Model: orSlug}},
				FallbackOn: OnLimitOrRefusal,
			},
		},
		DefaultProvider: "openrouter",
		SessionID:       func() string { return session },
		UserAgent:       userAgent,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return &fixture{router: New(cfg), goSrv: goSrv, orSrv: orSrv}
}

func keyFunc(key string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return key, nil }
}

func chatRequest(id, model string) provider.Request {
	return provider.Request{
		ModelID:  id,
		Model:    model,
		Messages: []message.Message{message.User("hi")},
	}
}

// wireModel reads the model field of a recorded JSON request body.
func wireModel(t *testing.T, body []byte) string {
	t.Helper()
	var decoded struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	return decoded.Model
}

func allowanceUsage() *provider.Usage {
	return &provider.Usage{PromptTokens: 32, CompletionTokens: 8, TotalTokens: 40,
		CostUSD: 0.00000576, CostSource: "allowance", AllowanceUSD: 0.00000576,
		ListCostUSD: 0.0000096, BillingSource: "go"}
}

func TestRouter_RT01_GoRouteReachesGo(t *testing.T) {
	fx := newFixture(t,
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "from go", Usage: allowanceUsage()}}},
		nil, nil)
	resp, err := fx.router.Stream(context.Background(), chatRequest(goID, goSlug), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Provider != "opencode-go" {
		t.Errorf("Provider = %q, want opencode-go", resp.Provider)
	}
	goReqs := fx.goSrv.Requests()
	if len(goReqs) != 1 {
		t.Fatalf("Go saw %d requests, want 1", len(goReqs))
	}
	got := goReqs[0]
	if !strings.HasSuffix(got.Path, "/chat/completions") {
		t.Errorf("path = %q, want the chat-completions suffix", got.Path)
	}
	if wireModel(t, got.Body) != goSlug {
		t.Errorf("wire model = %q, want the bare slug", wireModel(t, got.Body))
	}
	if got.Header.Get("Authorization") != "Bearer k-go" {
		t.Errorf("Authorization = %q", got.Header.Get("Authorization"))
	}
	if got.Header.Get("User-Agent") != userAgent {
		t.Errorf("User-Agent = %q, want the app agent", got.Header.Get("User-Agent"))
	}
	if got.Header.Get("x-opencode-session") != session {
		t.Errorf("x-opencode-session = %q, want %q", got.Header.Get("x-opencode-session"), session)
	}
	if len(fx.orSrv.Requests()) != 0 {
		t.Error("OpenRouter was called for a Go-served route")
	}
	u := resp.Usage
	if u.AllowanceUSD != 0.00000576 || u.CostSource != "allowance" || u.BillingSource != "go" {
		t.Errorf("usage = %+v, want the Go allowance numbers", u)
	}
}

func TestRouter_RT02_NoSessionIDStillSends(t *testing.T) {
	fx := newFixture(t, nil,
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "from or"}}},
		func(c *Config) {
			c.SessionID = nil
			c.Routes = map[string]Route{orOnly: {Attempts: []Attempt{{Provider: "openrouter", Model: orOnly}}}}
		})
	resp, err := fx.router.Stream(context.Background(), chatRequest(orOnly, orOnly), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !strings.Contains(resp.Message.Content, "from or") {
		t.Errorf("content = %q", resp.Message.Content)
	}
	orReqs := fx.orSrv.Requests()
	if len(orReqs) != 1 {
		t.Fatalf("OpenRouter saw %d requests, want 1", len(orReqs))
	}
	if h := orReqs[0].Header.Get("x-opencode-session"); h != "" {
		t.Errorf("x-opencode-session = %q, want no header without a session id", h)
	}
}

func TestRouter_RT03_LimitSpillsExactlyOnce(t *testing.T) {
	var fallback [3]string
	var calls int
	fx := newFixture(t,
		[]testkit.ServerTurn{{Fault: testkit.Fault429}},
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "from or"}}},
		func(c *Config) {
			r := c.Routes[goID]
			r.OnFallback = func(from, to string, err error) {
				calls++
				fallback = [3]string{from, to, err.Error()}
			}
			c.Routes[goID] = r
		})
	resp, err := fx.router.Stream(context.Background(), chatRequest(goID, goSlug), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !strings.Contains(resp.Message.Content, "from or") {
		t.Errorf("content = %q, want the OpenRouter answer", resp.Message.Content)
	}
	if resp.Provider != "openrouter" {
		t.Errorf("Provider = %q, want the attempt that answered", resp.Provider)
	}
	if len(fx.goSrv.Requests()) != 1 || len(fx.orSrv.Requests()) != 1 {
		t.Errorf("calls = %d go + %d or, want exactly one each",
			len(fx.goSrv.Requests()), len(fx.orSrv.Requests()))
	}
	if calls != 1 {
		t.Fatalf("OnFallback fired %d times, want once", calls)
	}
	if fallback[0] != "opencode-go" || fallback[1] != "openrouter" || fallback[2] == "" {
		t.Errorf("OnFallback = %v, want from/to/error", fallback)
	}
}

func TestRouter_RT04_BadRequestDoesNotSpill(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_request_error","message":"empty input"}}`)
	}))
	t.Cleanup(bad.Close)
	orSrv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "must not run"}})
	r := New(Config{
		Providers: []ProviderDecl{
			{Name: "openrouter", Kind: OpenRouter, BaseURL: orSrv.URL, APIKeyFunc: keyFunc("k-or")},
			{Name: "opencode-go", Kind: OpenCodeGo, BaseURL: bad.URL, APIKeyFunc: keyFunc("k-go")},
		},
		Routes: map[string]Route{
			goID: {Attempts: []Attempt{{Provider: "opencode-go", Model: goSlug}, {Provider: "openrouter", Model: orSlug}}},
		},
		DefaultProvider: "openrouter",
	})
	_, err := r.Stream(context.Background(), chatRequest(goID, goSlug), nil)
	if err == nil {
		t.Fatal("Stream succeeded, want the bad_request error")
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Status != 400 {
		t.Fatalf("err = %v, want the primary 400", err)
	}
	if len(orSrv.Requests()) != 0 {
		t.Errorf("OpenRouter saw %d requests, want none", len(orSrv.Requests()))
	}
}

func TestRouter_RT05_CancellationDoesNotSpill(t *testing.T) {
	fx := newFixture(t,
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "go"}}},
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "or"}}},
		nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fx.router.Stream(ctx, chatRequest(goID, goSlug), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the caller's cancellation", err)
	}
	if len(fx.orSrv.Requests()) != 0 {
		t.Errorf("OpenRouter saw %d requests after cancellation, want none", len(fx.orSrv.Requests()))
	}
}

func TestRouter_RT06_FailedAttemptUsageKept(t *testing.T) {
	usageLine := `data: {"choices":[],"usage":{"prompt_tokens":32,"completion_tokens":8,"total_tokens":40,"cost":0,"opencode":{"billing_source":"go","list_cost":0.0000096,"allowance":0.00000576}}}` + "\n\n"
	errLine := `data: {"error":{"code":"rate_limit_exceeded","message":"slow down"}}` + "\n\n"
	fx := newFixture(t,
		[]testkit.ServerTurn{{Raw: []string{usageLine, errLine}}},
		[]testkit.ServerTurn{{Turn: testkit.Turn{
			Text:  "from or",
			Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CostUSD: 0.001, CostSource: "provider"},
		}}},
		nil)
	resp, err := fx.router.Stream(context.Background(), chatRequest(goID, goSlug), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	u := resp.Usage
	if u.PromptTokens != 42 || u.CompletionTokens != 13 || u.TotalTokens != 55 {
		t.Errorf("tokens = %+v, want both attempts summed", u)
	}
	if u.AllowanceUSD != 0.00000576 || math.Abs(u.CostUSD-0.00100576) > 1e-12 {
		t.Errorf("cost = %+v, want allowance plus provider cost", u)
	}
	if u.CostSource != "provider" {
		t.Errorf("CostSource = %q, want provider for the mixed total", u.CostSource)
	}
}

func TestRouter_RT07_OnFallbackFiresOncePerSpill(t *testing.T) {
	newSpill := func(t *testing.T) (*fixture, *int) {
		t.Helper()
		var calls int
		return newFixture(t,
			[]testkit.ServerTurn{{Fault: testkit.Fault429}},
			[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "or"}}},
			func(c *Config) {
				r := c.Routes[goID]
				r.OnFallback = func(_, _ string, _ error) { calls++ }
				c.Routes[goID] = r
			}), &calls
	}
	fx, calls := newSpill(t)
	if _, err := fx.router.Stream(context.Background(), chatRequest(goID, goSlug), nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if *calls != 1 {
		t.Errorf("spill fired %d times, want once", *calls)
	}

	var quietCalls int
	quiet := newFixture(t,
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "go"}}},
		nil,
		func(c *Config) {
			r := c.Routes[goID]
			r.Attempts = r.Attempts[:1]
			r.OnFallback = func(_, _ string, _ error) { quietCalls++ }
			c.Routes[goID] = r
		})
	if _, err := quiet.router.Stream(context.Background(), chatRequest(goID, goSlug), nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if quietCalls != 0 {
		t.Errorf("OnFallback fired %d times on a primary success, want none", quietCalls)
	}
}

func TestRouter_RT08_ResolveAndDefault(t *testing.T) {
	fx := newFixture(t, nil, nil, nil)
	route, ok := fx.router.Resolve(goID)
	if !ok {
		t.Fatalf("Resolve(%q) ok = false, want the configured route", goID)
	}
	if len(route.Attempts) != 2 || route.Attempts[0].Provider != "opencode-go" || route.Attempts[1].Provider != "openrouter" {
		t.Errorf("attempts = %+v, want the configured order", route.Attempts)
	}
	if name, model, ok := fx.router.Primary(goID); !ok || name != "opencode-go" || model != goSlug {
		t.Errorf("Primary = %q/%q/%v, want opencode-go/%s/true", name, model, ok, goSlug)
	}
	if _, ok := fx.router.Resolve("nope/model"); ok {
		t.Errorf("Resolve(unknown) ok = true, want false")
	}
	name, _, _ := fx.router.Primary("nope/model")
	if name != "openrouter" {
		t.Errorf("unknown id primary = %q, want the configured default", name)
	}

	first := newFixture(t, nil, nil, func(c *Config) { c.DefaultProvider = "" })
	if name, _, _ := first.router.Primary("nope/model"); name != "openrouter" {
		t.Errorf("empty default primary = %q, want the first provider", name)
	}
}

func TestRouter_RT09_ConcurrentUse(t *testing.T) {
	const n = 8
	var ids atomic.Int64
	goTurns := make([]testkit.ServerTurn, 0, n)
	orTurns := make([]testkit.ServerTurn, 0, n)
	for range n {
		goTurns = append(goTurns, testkit.ServerTurn{Turn: testkit.Turn{Text: "go"}})
		orTurns = append(orTurns, testkit.ServerTurn{Turn: testkit.Turn{Text: "or"}})
	}
	routes := map[string]Route{}
	for i := range n {
		id := "test/model-" + string(rune('a'+i))
		name := "opencode-go"
		if i%2 == 1 {
			name = "openrouter"
		}
		routes[id] = Route{Attempts: []Attempt{{Provider: name, Model: "m"}}}
	}
	goSrv := testkit.NewGoServer(t, testkit.GoConfig{RequireSession: true}, goTurns...)
	orSrv := testkit.NewServer(t, orTurns...)
	r := New(Config{
		Providers: []ProviderDecl{
			{Name: "openrouter", Kind: OpenRouter, BaseURL: orSrv.URL, APIKeyFunc: keyFunc("k-or")},
			{Name: "opencode-go", Kind: OpenCodeGo, BaseURL: goSrv.URL, APIKeyFunc: keyFunc("k-go")},
		},
		Routes:          routes,
		DefaultProvider: "openrouter",
		SessionID:       func() string { return "t-" + string(rune('a'+int(ids.Add(1)-1))) },
	})
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "test/model-" + string(rune('a'+i))
			req := chatRequest(id, "m")
			_, errs[i] = r.Stream(context.Background(), req, nil)
			if len(req.ExtraHeaders) != 0 {
				t.Errorf("caller request mutated: %v", req.ExtraHeaders)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
	seen := map[string]bool{}
	for _, req := range goSrv.Requests() {
		h := req.Header.Get("x-opencode-session")
		if h == "" || seen[h] {
			t.Errorf("session header %q missing or shared", h)
		}
		seen[h] = true
	}
}

func TestRouter_RT10_NoFallbackUnchangedError(t *testing.T) {
	fx := newFixture(t,
		[]testkit.ServerTurn{{Fault: testkit.Fault500}},
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "must not run"}}},
		func(c *Config) {
			r := c.Routes[goID]
			r.Attempts = r.Attempts[:1]
			c.Routes[goID] = r
		})
	resp, err := fx.router.Stream(context.Background(), chatRequest(goID, goSlug), nil)
	if err == nil {
		t.Fatal("Stream succeeded, want the primary error")
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil with the error", resp)
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Status != 500 {
		t.Fatalf("err = %v, want the primary 500 unchanged", err)
	}
	if len(fx.orSrv.Requests()) != 0 {
		t.Errorf("OpenRouter saw %d requests, want none", len(fx.orSrv.Requests()))
	}
}

func TestRouter_RT12_AttributionIsTheServingAttempt(t *testing.T) {
	fx := newFixture(t,
		[]testkit.ServerTurn{{Fault: testkit.Fault429}},
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "or"}}},
		nil)
	if fx.router.Name() != "route" {
		t.Errorf("Name() = %q, want the stable router id", fx.router.Name())
	}
	resp, err := fx.router.Stream(context.Background(), chatRequest(goID, goSlug), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Provider != "openrouter" {
		t.Errorf("Provider = %q, want the attempt that answered", resp.Provider)
	}
}

func TestRouter_RT13_ForceProviderPinsEverything(t *testing.T) {
	fx := newFixture(t,
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "go"}}},
		[]testkit.ServerTurn{{Turn: testkit.Turn{Text: "or"}}},
		func(c *Config) { c.ForceProvider = "openrouter" })
	resp, err := fx.router.Stream(context.Background(), chatRequest(goID, goSlug), nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !strings.Contains(resp.Message.Content, "or") {
		t.Errorf("content = %q, want the pinned provider's answer", resp.Message.Content)
	}
	if len(fx.goSrv.Requests()) != 0 {
		t.Errorf("Go saw %d requests under the pin, want none", len(fx.goSrv.Requests()))
	}
	if resp.Provider != "openrouter" {
		t.Errorf("Provider = %q, want openrouter", resp.Provider)
	}
}
