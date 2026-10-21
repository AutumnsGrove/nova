package decide_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// fakeDecider records calls and plays fn.
type fakeDecider struct {
	mu    sync.Mutex
	calls []decide.Request
	fn    func(context.Context, decide.Request) (*decide.Response, error)
}

func (f *fakeDecider) Decide(ctx context.Context, req decide.Request) (*decide.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(ctx, req)
	}
	return &decide.Response{Model: req.Model}, nil
}

func (f *fakeDecider) models() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.Model
	}
	return out
}

func request(model string) decide.Request {
	return decide.Request{Model: model, State: "state", Questions: map[string]decide.Question{
		"q": {Instructions: "pick", Criteria: map[string]string{"a": "one", "b": "two"}},
	}}
}

func TestRoles_UnknownRoleIsANamedConfigurationError(t *testing.T) {
	f := &fakeDecider{}
	r := decide.NewRoles(decide.RolesConfig{
		Decider: f,
		Roles:   map[string]decide.Role{"oracle": {Model: "typesafe/jev-1.14"}},
	})
	_, err := r.Decide(context.Background(), request("orcale"))
	if err == nil || !strings.Contains(err.Error(), "orcale") || !strings.Contains(err.Error(), "role") {
		t.Fatalf("error = %v, want an unknown-role error naming orcale", err)
	}
	if got := f.models(); len(got) != 0 {
		t.Fatalf("decider was called with %v, want no call", got)
	}
}

func TestRoles_DirectModelIDPassesThrough(t *testing.T) {
	f := &fakeDecider{fn: func(context.Context, decide.Request) (*decide.Response, error) {
		return &decide.Response{Model: "vendor/build", Usage: provider.Usage{PromptTokens: 4}}, nil
	}}
	r := decide.NewRoles(decide.RolesConfig{Decider: f})
	resp, err := r.Decide(context.Background(), request("vendor/model-x"))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got := f.models(); len(got) != 1 || got[0] != "vendor/model-x" {
		t.Fatalf("decider saw %v, want the direct id", got)
	}
	if resp.Model != "vendor/build" || resp.Usage.PromptTokens != 4 {
		t.Fatalf("response = %+v, want the decider's model and usage", resp)
	}
}

func TestRoles_AliasIsRefusedByDefault(t *testing.T) {
	f := &fakeDecider{}
	r := decide.NewRoles(decide.RolesConfig{
		Decider: f,
		Roles: map[string]decide.Role{
			"oracle": {Model: "~typesafe/jev-latest"},
		},
	})
	_, err := r.Decide(context.Background(), request("oracle"))
	if err == nil || !strings.Contains(err.Error(), "oracle") || !strings.Contains(err.Error(), "jev-latest") || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("error = %v, want an alias error naming the role and id", err)
	}
	if got := f.models(); len(got) != 0 {
		t.Fatalf("decider was called with %v, want no call", got)
	}

	// A direct id is checked too, so an app cannot smuggle an alias past a role.
	_, err = r.Decide(context.Background(), request("typesafe/jev-latest"))
	if err == nil || !strings.Contains(err.Error(), "typesafe/jev-latest") || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("direct alias error = %v, want an alias error naming the id", err)
	}
}

func TestRoles_AllowAliasesOptsOut(t *testing.T) {
	f := &fakeDecider{}
	r := decide.NewRoles(decide.RolesConfig{
		Decider:      f,
		AllowAliases: true,
		Roles:        map[string]decide.Role{"oracle": {Model: "~typesafe/jev-latest"}},
	})
	if _, err := r.Decide(context.Background(), request("oracle")); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got := f.models(); len(got) != 1 || got[0] != "~typesafe/jev-latest" {
		t.Fatalf("decider saw %v, want the alias", got)
	}
}

func TestRoles_RoleWithoutAModelIsNamed(t *testing.T) {
	r := decide.NewRoles(decide.RolesConfig{Decider: &fakeDecider{}, Roles: map[string]decide.Role{"oracle": {}}})
	_, err := r.Decide(context.Background(), request("oracle"))
	if err == nil || !strings.Contains(err.Error(), "oracle") || !strings.Contains(err.Error(), "no model") {
		t.Fatalf("error = %v, want an error naming the role", err)
	}
}

func TestRoles_FallbackAnswersAndUsageIsSummed(t *testing.T) {
	f := &fakeDecider{fn: func(_ context.Context, req decide.Request) (*decide.Response, error) {
		switch req.Model {
		case "vendor/primary":
			return &decide.Response{Usage: provider.Usage{PromptTokens: 10, CostUSD: 0.1}}, errors.New("primary unavailable")
		case "vendor/fallback":
			return &decide.Response{Model: "vendor/fallback-build", Usage: provider.Usage{PromptTokens: 5, CostUSD: 0.2}}, nil
		default:
			t.Fatalf("unexpected model %q", req.Model)
			return nil, nil
		}
	}}
	r := decide.NewRoles(decide.RolesConfig{
		Decider: f,
		Roles:   map[string]decide.Role{"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback"}},
	})
	resp, err := r.Decide(context.Background(), request("oracle"))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Model != "vendor/fallback-build" {
		t.Errorf("Model = %q, want the fallback build", resp.Model)
	}
	if resp.Usage.PromptTokens != 15 || resp.Usage.CostUSD < 0.29 {
		t.Errorf("Usage = %+v, want both attempts summed", resp.Usage)
	}
}

func TestRoles_BothFailingReturnsTheFirstErrorAndUsage(t *testing.T) {
	errPrimary := errors.New("primary failed")
	errFallback := errors.New("fallback failed")
	f := &fakeDecider{fn: func(_ context.Context, req decide.Request) (*decide.Response, error) {
		switch req.Model {
		case "vendor/primary":
			return &decide.Response{Usage: provider.Usage{PromptTokens: 2}}, errPrimary
		case "vendor/fallback":
			return &decide.Response{Usage: provider.Usage{PromptTokens: 3}}, errFallback
		}
		return nil, nil
	}}
	r := decide.NewRoles(decide.RolesConfig{
		Decider: f,
		Roles:   map[string]decide.Role{"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback"}},
	})
	resp, err := r.Decide(context.Background(), request("oracle"))
	if !errors.Is(err, errPrimary) {
		t.Fatalf("error = %v, want the first error", err)
	}
	if resp == nil || resp.Usage.PromptTokens != 5 {
		t.Fatalf("response = %+v, want usage from both attempts", resp)
	}
}

func TestRoles_TimeoutIsATypedRetryableError(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(time.Unix(0, 0))
	f := &fakeDecider{fn: func(ctx context.Context, _ decide.Request) (*decide.Response, error) {
		<-ctx.Done()
		return &decide.Response{Usage: provider.Usage{PromptTokens: 7}}, ctx.Err()
	}}
	r := decide.NewRoles(decide.RolesConfig{
		Decider: f,
		Clock:   clock,
		Roles:   map[string]decide.Role{"oracle": {Model: "typesafe/jev-1.14", Timeout: 2500 * time.Millisecond}},
	})

	type result struct {
		resp *decide.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := r.Decide(context.Background(), request("oracle"))
		done <- result{resp, err}
	}()
	clock.BlockUntil(t, 1)
	clock.Advance(2500 * time.Millisecond)
	got := <-done

	var pe *provider.Error
	if !errors.As(got.err, &pe) || !pe.Retryable {
		t.Fatalf("error = %v, want a retryable provider error", got.err)
	}
	if !errors.Is(got.err, context.DeadlineExceeded) {
		t.Errorf("error does not unwrap to context.DeadlineExceeded: %v", got.err)
	}
	if got.resp == nil || got.resp.Usage.PromptTokens != 7 {
		t.Errorf("response = %+v, want the attempt's usage", got.resp)
	}
}

func TestRoles_TimeoutFallsBack(t *testing.T) {
	clock := testkit.NewFakeClock(time.Unix(0, 0))
	f := &fakeDecider{fn: func(ctx context.Context, req decide.Request) (*decide.Response, error) {
		if req.Model == "vendor/primary" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &decide.Response{Model: "vendor/fallback-build"}, nil
	}}
	r := decide.NewRoles(decide.RolesConfig{
		Decider: f,
		Clock:   clock,
		Roles:   map[string]decide.Role{"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback", Timeout: time.Second}},
	})
	type result struct {
		resp *decide.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := r.Decide(context.Background(), request("oracle"))
		done <- result{resp, err}
	}()
	clock.BlockUntil(t, 1)
	clock.Advance(time.Second)
	got := <-done
	if got.err != nil {
		t.Fatalf("Decide: %v", got.err)
	}
	if got.resp.Model != "vendor/fallback-build" {
		t.Errorf("Model = %q, want the fallback", got.resp.Model)
	}
}

func TestRoles_CallerCancellationDoesNotFallBack(t *testing.T) {
	f := &fakeDecider{fn: func(ctx context.Context, _ decide.Request) (*decide.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := decide.NewRoles(decide.RolesConfig{
		Decider: f,
		Roles:   map[string]decide.Role{"oracle": {Model: "vendor/primary", Fallback: "vendor/fallback"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Decide(ctx, request("oracle"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := f.models(); len(got) != 1 {
		t.Fatalf("decider calls = %v, want one attempt and no fallback", got)
	}
}

func TestRoles_DoesNotMutateTheCallersRequest(t *testing.T) {
	f := &fakeDecider{}
	r := decide.NewRoles(decide.RolesConfig{
		Decider: f,
		Roles:   map[string]decide.Role{"oracle": {Model: "typesafe/jev-1.14"}},
	})
	req := request("oracle")
	if _, err := r.Decide(context.Background(), req); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if req.Model != "oracle" {
		t.Fatalf("caller request Model changed to %q", req.Model)
	}
}

func TestRoles_NoDeciderIsAnError(t *testing.T) {
	r := decide.NewRoles(decide.RolesConfig{})
	if _, err := r.Decide(context.Background(), request("vendor/model")); err == nil || !strings.Contains(err.Error(), "decider") {
		t.Fatalf("error = %v, want a no-decider error", err)
	}
}

func TestRoles_NilResponseIsAnError(t *testing.T) {
	f := &fakeDecider{fn: func(context.Context, decide.Request) (*decide.Response, error) { return nil, nil }}
	r := decide.NewRoles(decide.RolesConfig{Decider: f})
	if _, err := r.Decide(context.Background(), request("vendor/model")); err == nil || !strings.Contains(err.Error(), "no response") {
		t.Fatalf("error = %v, want a no-response error", err)
	}
}
