package roles_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/roles"
	"github.com/AutumnsGrove/nova/testkit"
)

// recorder records the models Do attempted and plays fn.
type recorder struct {
	mu     sync.Mutex
	models []string
	fn     func(context.Context, string) (provider.Usage, error)
}

func (r *recorder) attempt(ctx context.Context, model string) (provider.Usage, error) {
	r.mu.Lock()
	r.models = append(r.models, model)
	r.mu.Unlock()
	if r.fn != nil {
		return r.fn(ctx, model)
	}
	return provider.Usage{}, nil
}

func (r *recorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.models...)
}

func TestRoles_UnknownBareNameIsANamedConfigurationError(t *testing.T) {
	r := &recorder{}
	table := roles.New(roles.Config{
		Subsystem: "decide",
		Roles:     map[string]roles.Role{"oracle": {Model: "typesafe/jev-1.14"}},
	})
	_, err := table.Do(context.Background(), "orcale", r.attempt)
	if err == nil || !strings.Contains(err.Error(), "decide: unknown role") || !strings.Contains(err.Error(), "orcale") {
		t.Fatalf("error = %v, want an unknown-role error naming orcale", err)
	}
	if got := r.calls(); len(got) != 0 {
		t.Fatalf("attempts = %v, want none", got)
	}
}

func TestRoles_SlashedIDPassesThroughWithNoTimeout(t *testing.T) {
	table := roles.New(roles.Config{Roles: map[string]roles.Role{"oracle": {Model: "vendor/primary", Timeout: time.Second}}})
	chain, err := table.Resolve("vendor/model-x")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(chain.Models) != 1 || chain.Models[0] != "vendor/model-x" {
		t.Fatalf("chain = %+v, want the direct id alone", chain)
	}
	if chain.Timeout != 0 {
		t.Fatalf("timeout = %v, want none for a direct id", chain.Timeout)
	}
}

func TestRoles_ResolveReturnsTheChainAndTimeout(t *testing.T) {
	table := roles.New(roles.Config{Roles: map[string]roles.Role{
		"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback", Timeout: 2 * time.Second},
	}})
	chain, err := table.Resolve("oracle")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(chain.Models) != 2 || chain.Models[0] != "vendor/primary" || chain.Models[1] != "vendor/fallback" {
		t.Fatalf("models = %v, want primary then fallback", chain.Models)
	}
	if chain.Timeout != 2*time.Second {
		t.Fatalf("timeout = %v, want 2s", chain.Timeout)
	}
}

func TestRoles_AliasIsRefusedForThePrimaryAndTheFallback(t *testing.T) {
	table := roles.New(roles.Config{Subsystem: "decide", Roles: map[string]roles.Role{
		"primary":  {Model: "vendor/model-latest"},
		"fallback": {Model: "vendor/model-x", Fallback: "~vendor/model-y"},
	}})
	for name, want := range map[string]string{"primary": `role "primary"`, "fallback": `role "fallback" fallback`} {
		_, err := table.Resolve(name)
		if err == nil || !strings.Contains(err.Error(), "alias") || !strings.Contains(err.Error(), want) {
			t.Fatalf("Resolve(%q) error = %v, want an alias error naming %q", name, err, want)
		}
	}
}

func TestRoles_AllowAliasesOptsOut(t *testing.T) {
	table := roles.New(roles.Config{AllowAliases: true, Roles: map[string]roles.Role{"oracle": {Model: "~vendor/model-latest"}}})
	if _, err := table.Resolve("oracle"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestRoles_RoleWithoutAModelIsNamed(t *testing.T) {
	table := roles.New(roles.Config{Roles: map[string]roles.Role{"oracle": {}}})
	_, err := table.Resolve("oracle")
	if err == nil || !strings.Contains(err.Error(), "oracle") || !strings.Contains(err.Error(), "no model") {
		t.Fatalf("error = %v, want an error naming the role", err)
	}
}

func TestRoles_DoStopsAtTheFirstSuccess(t *testing.T) {
	r := &recorder{}
	table := roles.New(roles.Config{Roles: map[string]roles.Role{
		"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback"},
	}})
	usage, err := table.Do(context.Background(), "oracle", r.attempt)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := r.calls(); len(got) != 1 || got[0] != "vendor/primary" {
		t.Fatalf("attempts = %v, want the primary only", got)
	}
	if usage != (provider.Usage{}) {
		t.Fatalf("usage = %+v, want the zero usage returned by the attempt", usage)
	}
}

func TestRoles_DoSumsUsageFromAFailedPrimary(t *testing.T) {
	r := &recorder{fn: func(_ context.Context, model string) (provider.Usage, error) {
		switch model {
		case "vendor/primary":
			return provider.Usage{PromptTokens: 10, CostUSD: 0.1}, errors.New("primary unavailable")
		case "vendor/fallback":
			return provider.Usage{PromptTokens: 5, CostUSD: 0.2}, nil
		}
		return provider.Usage{}, nil
	}}
	table := roles.New(roles.Config{Roles: map[string]roles.Role{
		"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback"},
	}})
	usage, err := table.Do(context.Background(), "oracle", r.attempt)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if usage.PromptTokens != 15 || usage.CostUSD < 0.29 {
		t.Fatalf("usage = %+v, want both attempts summed", usage)
	}
}

func TestRoles_DoReturnsTheFirstErrorWithThePartialUsage(t *testing.T) {
	errPrimary := errors.New("primary failed")
	errFallback := errors.New("fallback failed")
	r := &recorder{fn: func(_ context.Context, model string) (provider.Usage, error) {
		if model == "vendor/primary" {
			return provider.Usage{PromptTokens: 2}, errPrimary
		}
		return provider.Usage{PromptTokens: 3}, errFallback
	}}
	table := roles.New(roles.Config{Roles: map[string]roles.Role{
		"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback"},
	}})
	usage, err := table.Do(context.Background(), "oracle", r.attempt)
	if !errors.Is(err, errPrimary) {
		t.Fatalf("error = %v, want the first error", err)
	}
	if usage.PromptTokens != 5 {
		t.Fatalf("usage = %+v, want usage from both attempts", usage)
	}
}

func TestRoles_TimeoutIsATypedRetryableError(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(time.Unix(0, 0))
	table := roles.New(roles.Config{
		Subsystem: "decide",
		Clock:     clock,
		Roles:     map[string]roles.Role{"oracle": {Model: "typesafe/jev-1.14", Timeout: 2500 * time.Millisecond}},
	})

	type result struct {
		usage provider.Usage
		err   error
	}
	done := make(chan result, 1)
	go func() {
		usage, err := table.Do(context.Background(), "oracle", func(ctx context.Context, _ string) (provider.Usage, error) {
			<-ctx.Done()
			return provider.Usage{PromptTokens: 7}, ctx.Err()
		})
		done <- result{usage, err}
	}()
	clock.BlockUntil(t, 1)
	clock.Advance(2500 * time.Millisecond)
	got := <-done

	var pe *provider.Error
	if !errors.As(got.err, &pe) || !pe.Retryable || pe.Code != "timeout" {
		t.Fatalf("error = %v, want a retryable timeout provider error", got.err)
	}
	if !errors.Is(got.err, context.DeadlineExceeded) {
		t.Errorf("error does not unwrap to context.DeadlineExceeded: %v", got.err)
	}
	if got.usage.PromptTokens != 7 {
		t.Errorf("usage = %+v, want the attempt's usage", got.usage)
	}
}

func TestRoles_CallerCancellationStopsTheChainAndIsNotATimeout(t *testing.T) {
	r := &recorder{fn: func(ctx context.Context, _ string) (provider.Usage, error) {
		<-ctx.Done()
		return provider.Usage{}, ctx.Err()
	}}
	table := roles.New(roles.Config{Roles: map[string]roles.Role{
		"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback", Timeout: time.Minute},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := table.Do(ctx, "oracle", r.attempt)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	var pe *provider.Error
	if errors.As(err, &pe) {
		t.Fatalf("error = %v, want a cancellation, not a timeout", err)
	}
	if got := r.calls(); len(got) != 1 {
		t.Fatalf("attempts = %v, want one and no fallback", got)
	}
}

func TestRoles_FallbackRunsOnlyWhileTheCallersContextIsLive(t *testing.T) {
	r := &recorder{fn: func(_ context.Context, model string) (provider.Usage, error) {
		if model == "vendor/primary" {
			return provider.Usage{}, errors.New("primary unavailable")
		}
		return provider.Usage{}, nil
	}}
	table := roles.New(roles.Config{Roles: map[string]roles.Role{
		"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback"},
	}})
	if _, err := table.Do(context.Background(), "oracle", r.attempt); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := r.calls(); len(got) != 2 {
		t.Fatalf("attempts = %v, want primary and fallback while the context is live", got)
	}

	r2 := &recorder{fn: func(ctx context.Context, _ string) (provider.Usage, error) {
		<-ctx.Done()
		return provider.Usage{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = table.Do(ctx, "oracle", r2.attempt)
	if got := r2.calls(); len(got) != 1 {
		t.Fatalf("attempts = %v, want no fallback after a cancellation", got)
	}
}

func TestRoles_DoWithoutAnAttemptIsAnError(t *testing.T) {
	table := roles.New(roles.Config{Subsystem: "decide"})
	_, err := table.Do(context.Background(), "vendor/model", nil)
	if err == nil || !strings.Contains(err.Error(), "decide:") {
		t.Fatalf("error = %v, want a prefixed error", err)
	}
}
