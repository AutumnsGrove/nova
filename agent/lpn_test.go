package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// An assistant message with no content, parts or tool calls is recorded (the
// transcript is the caller's) but never sent: some providers reject it (#43).
func Test_LPN01_EmptyAssistantMessageIsRecordedButNotSent(t *testing.T) {
	cases := []struct {
		name string
		turn testkit.Turn
	}{
		{"nothing at all", testkit.Turn{}},
		{"reasoning only", testkit.Turn{Reasoning: "pondering"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := testkit.NewFakeClock(baseTime)
			p := testkit.NewProvider(clock, tc.turn, textTurn("now an answer"))
			var asked atomic.Bool
			cfg := Config{Provider: p, Model: "m", Clock: clock}
			cfg.Hooks.TurnEnd = func(_ context.Context, turn Turn) TurnDecision {
				if turn.Index == 0 && !asked.Swap(true) {
					return TurnDecision{Continue: true, Inject: []message.Message{message.User("answer, please")}}
				}
				return TurnDecision{}
			}
			res, _ := runCollect(t, cfg, nil, message.User("go"))

			reqs := p.Requests()
			if len(reqs) != 2 {
				t.Fatalf("requests = %d, want 2", len(reqs))
			}
			for _, m := range reqs[1].Messages {
				if m.Role == message.RoleAssistant {
					t.Fatalf("follow-up request carries an empty assistant message: %+v", reqs[1].Messages)
				}
			}
			if !hasMessageContent(reqs[1].Messages, "answer, please") {
				t.Fatalf("injected message missing from the follow-up: %+v", reqs[1].Messages)
			}
			var stored int
			for _, m := range res.Messages {
				if m.Role == message.RoleAssistant && m.Content == "" {
					stored++
				}
			}
			if stored != 1 {
				t.Errorf("transcript holds %d empty assistant messages, want the 1 that was recorded", stored)
			}
		})
	}
}

func Test_LPN02_EmptyAssistantFilterKeepsRealMessages(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	history := []message.Message{
		message.User("q"),
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "c1", Type: "function", Function: message.FunctionCall{Name: "echo", Arguments: "{}"}}}},
		message.ToolResult("c1", "r"),
		message.UserParts("look").WithParts(message.ImagePart("image/png", "AAAA")),
		{Role: message.RoleAssistant, Parts: []message.ContentPart{message.ImagePart("image/png", "AAAA")}},
		message.Assistant("text"),
	}
	runCollect(t, cfg, history, message.User("go"))
	got := p.Requests()[0].Messages
	if len(got) != len(history)+1 {
		t.Fatalf("request has %d messages, want all %d kept: %+v", len(got), len(history)+1, got)
	}
}

func Test_LPN03_TransformContextStillSeesTheEmptyAssistantMessage(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	var sawEmpty bool
	cfg.Hooks.TransformContext = func(_ context.Context, msgs []message.Message) []message.Message {
		for _, m := range msgs {
			if m.Role == message.RoleAssistant && m.Content == "" && len(m.ToolCalls) == 0 {
				sawEmpty = true
			}
		}
		return nil
	}
	runCollect(t, cfg, []message.Message{message.User("q"), {Role: message.RoleAssistant}}, message.User("go"))
	if !sawEmpty {
		t.Fatal("the hook did not see the empty assistant message: the filter must run after it")
	}
}

// --- RequestTimeout (#44) ---------------------------------------------------

func Test_LPN04_RequestTimeoutFailsAStalledCall(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Hang: true})
	cfg := Config{Provider: p, Model: "m", Clock: clock, RequestTimeout: 20 * time.Millisecond}
	res, _, err := runCollectErr(t, cfg, nil, message.User("go"))
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error", err)
	}
	if res.Reason != "error" {
		t.Errorf("Reason = %q, want error (a stalled call is a failure, not an abort)", res.Reason)
	}
}

func Test_LPN05_RequestTimeoutLeavesAFastCallAlone(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("quick"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, RequestTimeout: time.Minute}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Reason != "done" || res.Answer != "quick" {
		t.Errorf("Reason = %q, Answer = %q", res.Reason, res.Answer)
	}
}

func Test_LPN06_RunCancellationIsStillAnAbortWithATimeoutSet(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Hang: true})
	cfg := Config{Provider: p, Model: "m", Clock: clock, RequestTimeout: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	res, err := New(cfg).Run(ctx, nil, message.User("go"))
	if res.Reason != "aborted" || !errors.Is(err, context.Canceled) {
		t.Errorf("Reason = %q, err = %v, want aborted/canceled", res.Reason, err)
	}
}

// Every attempt gets its own deadline: a retryable failure that took most of
// one budget must not leave the retry with the remainder.
func Test_LPN07_EachAttemptGetsItsOwnDeadline(t *testing.T) {
	var deadlines []time.Time
	p := &deadlineProvider{record: func(d time.Time, ok bool) {
		if ok {
			deadlines = append(deadlines, d)
		}
	}}
	cfg := Config{Provider: p, Model: "m", Clock: instantClock{}, RequestTimeout: time.Hour, Retry: RetryPolicy{Max: 1, Base: time.Millisecond, Jitter: -1}}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Reason != "done" {
		t.Fatalf("Reason = %q", res.Reason)
	}
	if len(deadlines) != 2 {
		t.Fatalf("saw %d deadlines, want one per attempt (2)", len(deadlines))
	}
	if !deadlines[1].After(deadlines[0]) {
		t.Errorf("second attempt reused the first deadline: %v then %v", deadlines[0], deadlines[1])
	}
}

type deadlineProvider struct {
	n      int
	record func(time.Time, bool)
}

func (p *deadlineProvider) Name() string { return "deadline" }

func (p *deadlineProvider) Stream(ctx context.Context, _ provider.Request, _ func(provider.StreamEvent)) (*provider.Response, error) {
	d, ok := ctx.Deadline()
	p.record(d, ok)
	p.n++
	if p.n == 1 {
		return nil, &provider.Error{Status: 503, Message: "busy", Retryable: true}
	}
	return &provider.Response{Message: message.Assistant("ok")}, nil
}

// instantClock reads the wall clock but never waits, so a retry's backoff costs
// no real time.
type instantClock struct{}

func (instantClock) Now() time.Time                             { return time.Now() }
func (instantClock) Sleep(context.Context, time.Duration) error { return nil }

// --- Result.Final (#45) -----------------------------------------------------

func Test_LPN08_FinalIsTheClosingAssistantText(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	res, _ := runCollect(t, Config{Provider: testkit.NewProvider(clock, textTurn("hello")), Model: "m", Clock: clock}, nil, message.User("go"))
	if res.Final != "hello" || res.Answer != "hello" {
		t.Errorf("Final = %q, Answer = %q, want both hello", res.Final, res.Answer)
	}
}

func Test_LPN09_FinalIsEmptyWhenTheLastMessageIsEmptyButAnswerIsStale(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{Text: "Let me look.", ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}},
		testkit.Turn{}, // the model then says nothing at all
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Answer != "Let me look." {
		t.Fatalf("Answer = %q, want the earlier remark (the documented behaviour)", res.Answer)
	}
	if res.Final != "" {
		t.Errorf("Final = %q, want empty: the run ended on an empty message", res.Final)
	}
}

func Test_LPN10_FinalIsEmptyWhenTheRunEndsOnAToolBatch(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	cfg := Config{Provider: testkit.NewProvider(clock, alwaysTool(3)...), Model: "m", Clock: clock, Tools: registry(echoTool()), MaxTurns: 2}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if !res.Exhausted || res.Final != "" {
		t.Errorf("Exhausted = %v, Final = %q, want exhausted with no final text", res.Exhausted, res.Final)
	}
}

func Test_LPN11_FinalIsTheWrapUpFallback(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	turns := append(alwaysTool(2), testkit.Turn{ToolCalls: []testkit.Call{toolCall("w", "echo", `{}`)}})
	cfg := Config{Provider: testkit.NewProvider(clock, turns...), Model: "m", Clock: clock, Tools: registry(echoTool()), MaxTurns: 2,
		WrapUp: &WrapUp{Fallback: "gave up"}}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Final != "gave up" {
		t.Errorf("Final = %q, want the wrap-up fallback", res.Final)
	}
}
