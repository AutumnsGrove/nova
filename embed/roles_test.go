package embed_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/embed"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// fakeEmbedder records the model of every call and plays fn.
type fakeEmbedder struct {
	mu     sync.Mutex
	models []string
	fn     func(context.Context, embed.Request) (*embed.Response, error)
}

func (f *fakeEmbedder) Embed(ctx context.Context, req embed.Request) (*embed.Response, error) {
	f.mu.Lock()
	f.models = append(f.models, req.Model)
	f.mu.Unlock()
	return f.fn(ctx, req)
}

func (f *fakeEmbedder) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.models...)
}

func okResponse(model string, dims int, usage provider.Usage) *embed.Response {
	vector := make([]float32, dims)
	for i := range vector {
		vector[i] = 1
	}
	return &embed.Response{Vectors: [][]float32{vector}, Model: model, Dims: dims, Usage: usage}
}

func embedRequest(model string) embed.Request {
	return embed.Request{Model: model, Input: []string{"a"}}
}

// TestEmbed_EMB11_RoleTimeoutIsATypedRetryableError drives the per-attempt
// deadline from the injected clock.
func TestEmbed_EMB11_RoleTimeoutIsATypedRetryableError(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(time.Unix(0, 0))
	f := &fakeEmbedder{fn: func(ctx context.Context, _ embed.Request) (*embed.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := embed.NewRoles(embed.RolesConfig{
		Embedder: f,
		Clock:    clock,
		Roles:    map[string]embed.Role{"index": {Model: "vendor/embed", Timeout: 2 * time.Second, Dims: 4}},
	})

	type result struct {
		resp *embed.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := r.Embed(context.Background(), embedRequest("index"))
		done <- result{resp, err}
	}()
	clock.BlockUntil(t, 1)
	clock.Advance(2 * time.Second)
	got := <-done

	var pe *provider.Error
	if !errors.As(got.err, &pe) || !pe.Retryable || pe.Code != "timeout" {
		t.Fatalf("error = %v, want a retryable timeout provider error", got.err)
	}
	if !errors.Is(got.err, context.DeadlineExceeded) {
		t.Errorf("error does not unwrap to context.DeadlineExceeded: %v", got.err)
	}
}

// TestEmbed_EMB11_FallbackRunsOnlyWhileTheContextIsLive pins the fallback rule.
func TestEmbed_EMB11_FallbackRunsOnlyWhileTheContextIsLive(t *testing.T) {
	f := &fakeEmbedder{fn: func(_ context.Context, req embed.Request) (*embed.Response, error) {
		if req.Model == "vendor/primary" {
			return nil, errors.New("primary unavailable")
		}
		return okResponse(req.Model, 2, provider.Usage{}), nil
	}}
	r := embed.NewRoles(embed.RolesConfig{
		Embedder: f,
		Roles:    map[string]embed.Role{"index": {Model: "vendor/primary", Fallback: "vendor/fallback", Dims: 2}},
	})
	if _, err := r.Embed(context.Background(), embedRequest("index")); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if got := f.calls(); len(got) != 2 {
		t.Fatalf("calls = %v, want primary then fallback while the context is live", got)
	}

	cancelled := &fakeEmbedder{fn: func(ctx context.Context, _ embed.Request) (*embed.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r = embed.NewRoles(embed.RolesConfig{
		Embedder: cancelled,
		Roles:    map[string]embed.Role{"index": {Model: "vendor/primary", Fallback: "vendor/fallback"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Embed(ctx, embedRequest("index")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := cancelled.calls(); len(got) != 1 {
		t.Fatalf("calls = %v, want no fallback after a cancellation", got)
	}
}

// TestEmbed_EMB12_FallbackWithDifferentDimsIsRefused checks the typed error, the
// two names, and that no further request is made.
func TestEmbed_EMB12_FallbackWithDifferentDimsIsRefused(t *testing.T) {
	f := &fakeEmbedder{fn: func(_ context.Context, req embed.Request) (*embed.Response, error) {
		if req.Model == "vendor/primary" {
			return nil, errors.New("primary unavailable")
		}
		return okResponse(req.Model, 768, provider.Usage{}), nil
	}}
	r := embed.NewRoles(embed.RolesConfig{
		Embedder: f,
		Roles:    map[string]embed.Role{"index": {Model: "vendor/primary", Fallback: "vendor/fallback", Dims: 1024}},
	})
	_, err := r.Embed(context.Background(), embedRequest("index"))
	var dimsErr *embed.DimsError
	if !errors.As(err, &dimsErr) {
		t.Fatalf("error = %v, want *embed.DimsError", err)
	}
	if dimsErr.Want != 1024 || dimsErr.Got != 768 {
		t.Fatalf("error = %+v, want 1024 and 768", dimsErr)
	}
	for _, want := range []string{"vendor/primary", "vendor/fallback"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if got := f.calls(); len(got) != 2 {
		t.Fatalf("calls = %v, want the primary and one fallback, then stop", got)
	}
}

// TestEmbed_EMB12_PrimaryDimsMismatchStopsWithNoFallback proves the check runs
// even when a fallback exists.
func TestEmbed_EMB12_PrimaryDimsMismatchStopsWithNoFallback(t *testing.T) {
	f := &fakeEmbedder{fn: func(_ context.Context, req embed.Request) (*embed.Response, error) {
		return okResponse(req.Model, 768, provider.Usage{}), nil
	}}
	r := embed.NewRoles(embed.RolesConfig{
		Embedder: f,
		Roles:    map[string]embed.Role{"index": {Model: "vendor/primary", Fallback: "vendor/fallback", Dims: 1024}},
	})
	var dimsErr *embed.DimsError
	if _, err := r.Embed(context.Background(), embedRequest("index")); !errors.As(err, &dimsErr) {
		t.Fatalf("error = %v, want *embed.DimsError", err)
	}
	if got := f.calls(); len(got) != 1 {
		t.Fatalf("calls = %v, want no fallback after a dimension mismatch", got)
	}
}

// TestEmbed_EMB13_AliasIsRefusedUnlessAllowed covers the primary and the
// fallback, and the opt-out.
func TestEmbed_EMB13_AliasIsRefusedUnlessAllowed(t *testing.T) {
	f := &fakeEmbedder{fn: func(_ context.Context, req embed.Request) (*embed.Response, error) {
		return okResponse(req.Model, 2, provider.Usage{}), nil
	}}
	r := embed.NewRoles(embed.RolesConfig{
		Embedder: f,
		Roles: map[string]embed.Role{
			"latest":          {Model: "vendor/embed-latest"},
			"latest-fallback": {Model: "vendor/embed", Fallback: "~vendor/embed-x"},
		},
	})
	for _, name := range []string{"latest", "latest-fallback"} {
		if _, err := r.Embed(context.Background(), embedRequest(name)); err == nil || !strings.Contains(err.Error(), "alias") {
			t.Fatalf("%s: error = %v, want an alias refusal", name, err)
		}
	}
	if got := f.calls(); len(got) != 0 {
		t.Fatalf("calls = %v, want none", got)
	}

	allowed := embed.NewRoles(embed.RolesConfig{
		Embedder:     f,
		AllowAliases: true,
		Roles:        map[string]embed.Role{"latest": {Model: "vendor/embed-latest", Dims: 2}},
	})
	if _, err := allowed.Embed(context.Background(), embedRequest("latest")); err != nil {
		t.Fatalf("Embed with allow_aliases: %v", err)
	}
}
