package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

func retryable() error { return &provider.Error{Status: 500, Retryable: true} }

func nonRetryable() error { return &provider.Error{Status: 401, Retryable: false} }

func failTurns(n int, err error) []testkit.Turn {
	turns := make([]testkit.Turn, n)
	for i := range turns {
		turns[i] = testkit.Turn{Err: err}
	}
	return turns
}

// advanceN pumps the fake clock whenever the run asks to sleep, n times. The
// step is deliberately huge so it fires a timer of any delay in this test file.
func advanceN(t *testing.T, clock *testkit.FakeClock, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		clock.BlockUntil(t, 1)
		clock.Advance(time.Hour)
	}
}

func retryEventDelays(events []proto.Event) []int {
	var out []int
	for _, e := range events {
		if e.Type == proto.EventRetry {
			out = append(out, e.DelayMS)
		}
	}
	return out
}

func Test_LPR01_RetryThenSuccess(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Err: retryable()}, textTurn("ok"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Retry: RetryPolicy{Max: 3, Base: time.Second, Cap: 30 * time.Second, Jitter: 0}}
	c := &collector{}
	cfg.Emit = c.emit
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	advanceN(t, clock, 1)
	out := <-done
	if out.err != nil || out.res.Answer != "ok" {
		t.Fatalf("result = %+v err = %v", out.res, out.err)
	}
	if got := countType(c.all(), proto.EventRetry); got != 1 {
		t.Fatalf("retry events = %d, want 1", got)
	}
}

func Test_LPR02_RetriesExhausted(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, failTurns(3, retryable())...)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Retry: RetryPolicy{Max: 2, Base: time.Second, Cap: 30 * time.Second, Jitter: 0}}
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	advanceN(t, clock, 2)
	out := <-done
	if out.err == nil || out.res.Reason != "error" {
		t.Fatalf("err = %v reason = %q, want error", out.err, out.res.Reason)
	}
	if got := len(p.Requests()); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func Test_LPR03_NonRetryable(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Err: nonRetryable()}, textTurn("ok"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Retry: RetryPolicy{Max: 3, Base: time.Second, Jitter: 0}}
	res, events, err := runCollectErr(t, cfg, nil, message.User("go"))
	if err == nil || res.Reason != "error" {
		t.Fatalf("err = %v reason = %q", err, res.Reason)
	}
	if got := len(p.Requests()); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	if countType(events, proto.EventRetry) != 0 {
		t.Fatal("non-retryable error produced a retry event")
	}
}

func Test_LPR04_BackoffDelays(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, failTurns(5, retryable())...)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Retry: RetryPolicy{Max: 4, Base: time.Second, Cap: 4 * time.Second, Jitter: 0}}
	c := &collector{}
	cfg.Emit = c.emit
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	advanceN(t, clock, 4)
	<-done
	got := retryEventDelays(c.all())
	want := []int{1000, 2000, 4000, 4000}
	if len(got) != len(want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delays = %v, want %v", got, want)
		}
	}
}

func Test_LPR05_RetryAfterRespected(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Err: &provider.Error{Status: 429, Retryable: true, RetryAfter: 5 * time.Second}}, textTurn("ok"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Retry: RetryPolicy{Max: 3, Base: time.Second, Cap: 30 * time.Second, Jitter: 0}}
	c := &collector{}
	cfg.Emit = c.emit
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	advanceN(t, clock, 1)
	<-done
	delays := retryEventDelays(c.all())
	if len(delays) != 1 || delays[0] < 5000 {
		t.Fatalf("delay = %v, want >= 5000", delays)
	}
}

func Test_LPR06_RetryAfterCapped(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Err: &provider.Error{Status: 429, Retryable: true, RetryAfter: time.Hour}}, textTurn("ok"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Retry: RetryPolicy{Max: 3, Base: time.Second, Cap: 30 * time.Second, Jitter: 0}}
	c := &collector{}
	cfg.Emit = c.emit
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	advanceN(t, clock, 1)
	<-done
	delays := retryEventDelays(c.all())
	if len(delays) != 1 || delays[0] != 5*60*1000 {
		t.Fatalf("delay = %v, want 300000", delays)
	}
}

func Test_LPR07_CancelDuringBackoff(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Err: retryable()}, textTurn("ok"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Retry: RetryPolicy{Max: 3, Base: time.Hour, Cap: time.Hour, Jitter: 0}}
	a := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan runOutcome, 1)
	go func() { done <- runAsyncCtx(a, ctx) }()
	clock.BlockUntil(t, 1) // the run is now sleeping in backoff
	cancel()
	out := <-done
	if !errors.Is(out.err, context.Canceled) || out.res.Reason != "aborted" {
		t.Fatalf("err = %v reason = %q, want aborted", out.err, out.res.Reason)
	}
}

func Test_LPR08_PartialOutputDiscarded(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{Text: "partial", Err: retryable(), ErrAfter: 1},
		textTurn("success"),
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Retry: RetryPolicy{Max: 3, Base: time.Second, Cap: 30 * time.Second, Jitter: 0}}
	c := &collector{}
	cfg.Emit = c.emit
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	advanceN(t, clock, 1)
	out := <-done
	if out.err != nil || out.res.Answer != "success" {
		t.Fatalf("result = %+v err = %v", out.res, out.err)
	}
	if messageHas(out.res.Messages, "partial") {
		t.Fatalf("partial output kept: %+v", out.res.Messages)
	}
	if countType(c.all(), proto.EventRetry) != 1 {
		t.Fatal("no retry event")
	}
}

func Test_LPR09_NegativeMaxDisables(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Err: retryable()}, textTurn("ok"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Retry: RetryPolicy{Max: -1}}
	res, _, err := runCollectErr(t, cfg, nil, message.User("go"))
	if err == nil || res.Reason != "error" {
		t.Fatalf("err = %v reason = %q", err, res.Reason)
	}
	if got := len(p.Requests()); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func Test_LPR10_JitterBounds(t *testing.T) {
	policy := RetryPolicy{Max: 3, Base: time.Second, Cap: time.Hour, Jitter: 0.5}
	for i := 0; i < 1000; i++ {
		d := retryDelay(policy, nil, 0)
		if d < time.Second || d > 1500*time.Millisecond {
			t.Fatalf("delay = %v, want within [1s, 1.5s]", d)
		}
	}
}

func Test_LPR11_ErrorAfterTools(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}, Usage: &provider.Usage{PromptTokens: 7, CompletionTokens: 1}},
		testkit.Turn{Err: nonRetryable()},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	res, _, err := runCollectErr(t, cfg, nil, message.User("go"))
	if err == nil || res.Reason != "error" {
		t.Fatalf("err = %v reason = %q", err, res.Reason)
	}
	assertValid(t, res.Messages)
	last := res.Messages[len(res.Messages)-1]
	if last.Role != message.RoleTool {
		t.Fatalf("transcript does not end with tool results: %+v", res.Messages)
	}
	if res.Usage.PromptTokens != 7 {
		t.Fatalf("earlier usage lost: %+v", res.Usage)
	}
}
