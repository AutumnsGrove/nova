package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

// shownText joins every live text_delta in order.
func shownText(events []proto.Event) string {
	var b strings.Builder
	for _, e := range events {
		if e.Type == proto.EventTextDelta {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

func Test_LPS11_StreamDeltaRewritesLiveText(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hello"})
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.StreamDelta = func(_ context.Context, ev StreamDelta) StreamDecision {
		return StreamDecision{Action: DeltaPass, Text: strings.ToUpper(ev.Text)}
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	if got := shownText(events); got != "HELLO" {
		t.Fatalf("shown = %q, want the rewrite", got)
	}
	if res.Answer != "hello" {
		t.Fatalf("answer = %q, want the original", res.Answer)
	}
}

func Test_LPS12_StreamDeltaHoldThenPass(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{TextChunks: []string{"he", "llo", " world"}})
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	calls := 0
	cfg.Hooks.StreamDelta = func(context.Context, StreamDelta) StreamDecision {
		calls++
		if calls < 3 {
			return StreamDecision{Action: DeltaHold}
		}
		return StreamDecision{Action: DeltaPass}
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	if got := shownText(events); got != "hello world" {
		t.Fatalf("shown = %q, want the held text released once", got)
	}
	if got := countType(events, proto.EventTextDelta); got != 1 {
		t.Errorf("text_delta count = %d, want one release", got)
	}
	if res.Answer != "hello world" {
		t.Fatalf("answer = %q", res.Answer)
	}
}

func Test_LPS13_StreamDeltaDropDiscardsHeldText(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{TextChunks: []string{"he", "llo", " world"}})
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	calls := 0
	cfg.Hooks.StreamDelta = func(context.Context, StreamDelta) StreamDecision {
		calls++
		switch calls {
		case 1:
			return StreamDecision{Action: DeltaHold}
		case 2:
			return StreamDecision{Action: DeltaDrop}
		default:
			return StreamDecision{Action: DeltaPass}
		}
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	if got := shownText(events); got != " world" {
		t.Fatalf("shown = %q, want the held prefix dropped", got)
	}
	if res.Answer != "hello world" {
		t.Fatalf("answer = %q, want the original", res.Answer)
	}
}

func Test_LPS14_StreamDeltaHeldAtEndIsFlushed(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hello"})
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.StreamDelta = func(context.Context, StreamDelta) StreamDecision {
		return StreamDecision{Action: DeltaHold}
	}

	_, events := runCollect(t, cfg, nil, message.User("go"))
	if got := shownText(events); got != "hello" {
		t.Fatalf("shown = %q, want the flush at message end", got)
	}
	textAt, endAt := -1, -1
	for i, e := range events {
		if e.Type == proto.EventTextDelta && textAt < 0 {
			textAt = i
		}
		if e.Type == proto.EventMessageEnd {
			endAt = i
		}
	}
	if textAt < 0 || endAt < 0 || textAt > endAt {
		t.Fatalf("held text was not released before message_end: %v", eventTypes(events))
	}
}

func Test_LPS15_StreamDeltaPanicPassesThrough(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hello"})
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.StreamDelta = func(context.Context, StreamDelta) StreamDecision {
		panic("boom")
	}

	_, events := runCollect(t, cfg, nil, message.User("go"))
	if got := shownText(events); got != "hello" {
		t.Fatalf("shown = %q, want the unmodified text", got)
	}
	found := false
	for _, e := range events {
		if e.Type == proto.EventExtensionError && e.Hook == "stream_delta" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no stream_delta extension_error: %v", eventTypes(events))
	}
}

func Test_LPS16_StreamDeltaTimeoutPassesThrough(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hello"})
	cfg := Config{Provider: p, Model: "m", Clock: clock, HookTimeout: 50 * time.Millisecond}
	cfg.Hooks.StreamDelta = func(ctx context.Context, _ StreamDelta) StreamDecision {
		<-ctx.Done()
		return StreamDecision{Action: DeltaPass}
	}

	_, events := runCollect(t, cfg, nil, message.User("go"))
	if got := shownText(events); got != "hello" {
		t.Fatalf("shown = %q, want the text after the deadline", got)
	}
	found := false
	for _, e := range events {
		if e.Type == proto.EventExtensionError && e.Hook == "stream_delta" && e.Error != nil && strings.Contains(e.Error.Message, "deadline") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no deadline extension_error: %v", eventTypes(events))
	}
}

func Test_LPS17_FilterThenHookComposition(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{TextChunks: []string{"he", "llo", " world"}})
	cfg := Config{
		Provider: p, Model: "m", Clock: clock,
		TextFilter: func() StreamFilter { return &holdPrefixFilter{prefix: "<tool_call>"} },
	}
	var seen []string
	cfg.Hooks.StreamDelta = func(_ context.Context, ev StreamDelta) StreamDecision {
		seen = append(seen, ev.Text)
		return StreamDecision{Action: DeltaPass, Text: strings.ToUpper(ev.Text)}
	}

	_, events := runCollect(t, cfg, nil, message.User("go"))
	if len(seen) != 1 || seen[0] != "hello world" {
		t.Fatalf("hook saw %v, want only the filter's released buffer", seen)
	}
	if got := shownText(events); got != "HELLO WORLD" {
		t.Fatalf("shown = %q, want the hook after the filter", got)
	}
}

func Test_LPS18_StreamDeltaSeesBothKinds(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Reasoning: "think", Text: "answer"})
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	var kinds []StreamKind
	cfg.Hooks.StreamDelta = func(_ context.Context, ev StreamDelta) StreamDecision {
		kinds = append(kinds, ev.Kind)
		return StreamDecision{Action: DeltaPass}
	}

	runCollect(t, cfg, nil, message.User("go"))
	if len(kinds) != 2 || kinds[0] != StreamReasoning || kinds[1] != StreamText {
		t.Fatalf("hook kinds = %v, want reasoning then text", kinds)
	}
}

func Test_LPS19_HookHoldCancellationDoesNotLeak(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	p := &holdProvider{emitted: make(chan struct{})}
	cfg := Config{Provider: p, Model: "m", Clock: clock}
	cfg.Hooks.StreamDelta = func(context.Context, StreamDelta) StreamDecision {
		return StreamDecision{Action: DeltaHold}
	}
	c := &collector{}
	cfg.Emit = c.emit

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var res *Result
	var err error
	go func() {
		res, err = New(cfg).Run(ctx, nil, message.User("go"))
		close(done)
	}()
	<-p.emitted
	cancel()
	<-done

	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("error = %v, want cancellation", err)
	}
	if res == nil || res.Reason != "aborted" {
		t.Fatalf("result = %+v, want an aborted run", res)
	}
	if got := countType(c.all(), proto.EventTextDelta); got != 0 {
		t.Errorf("text_delta count = %d, want none while held", got)
	}
}
