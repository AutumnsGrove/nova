package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// holdPrefixFilter is the shape Polaris's stream sniffer needs: hold chunks
// until the buffer is long enough to tell whether the answer starts with a
// pseudo-tool-call prefix, then either release the buffer or drop the rest.
type holdPrefixFilter struct {
	prefix   string
	buf      strings.Builder
	resolved bool
	drop     bool
}

func (f *holdPrefixFilter) Delta(_ context.Context, text string) string {
	if f.resolved {
		if f.drop {
			return ""
		}
		return text
	}
	f.buf.WriteString(text)
	if f.buf.Len() < len(f.prefix) {
		return ""
	}
	f.resolved = true
	if strings.HasPrefix(f.buf.String(), f.prefix) {
		f.drop = true
		return ""
	}
	out := f.buf.String()
	f.buf.Reset()
	return out
}

func (f *holdPrefixFilter) Flush(context.Context) string {
	if f.resolved {
		return ""
	}
	f.resolved = true
	out := f.buf.String()
	f.buf.Reset()
	return out
}

// dropFilter holds and then drops everything.
type dropFilter struct{}

func (dropFilter) Delta(context.Context, string) string { return "" }
func (dropFilter) Flush(context.Context) string         { return "" }

// rewriteFilter applies fn to each delta.
type rewriteFilter struct{ fn func(string) string }

func (f rewriteFilter) Delta(_ context.Context, text string) string { return f.fn(text) }
func (rewriteFilter) Flush(context.Context) string                  { return "" }

// panicFilter panics in Delta.
type panicFilter struct{}

func (panicFilter) Delta(context.Context, string) string { panic("boom") }
func (panicFilter) Flush(context.Context) string         { return "" }

func Test_LPS01_FilterHoldsThenReleasesInOrder(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{TextChunks: []string{"he", "llo", " world"}})
	cfg := Config{
		Provider: p, Model: "m", Clock: clock,
		TextFilter: func() StreamFilter { return &holdPrefixFilter{prefix: "<tool_call>"} },
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	if res.Answer != "hello world" {
		t.Fatalf("answer = %q", res.Answer)
	}
	var shown strings.Builder
	for _, e := range events {
		if e.Type == proto.EventTextDelta {
			shown.WriteString(e.Text)
		}
	}
	if shown.String() != "hello world" {
		t.Fatalf("shown = %q, want the whole answer once", shown.String())
	}
	if got := countType(events, proto.EventTextDelta); got != 1 {
		t.Errorf("text_delta count = %d, want one released buffer", got)
	}
}

func Test_LPS02_FilterSuppressesAPseudoCall(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	chunks := []string{"<tool_call>", "\nweb_search({\"q\":\"x\"})", "\n</tool_call>"}
	p := testkit.NewProvider(clock, testkit.Turn{TextChunks: chunks})
	cfg := Config{
		Provider: p, Model: "m", Clock: clock,
		TextFilter: func() StreamFilter { return &holdPrefixFilter{prefix: "<tool_call>"} },
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	if got := countType(events, proto.EventTextDelta); got != 0 {
		t.Fatalf("text_delta count = %d, want none", got)
	}
	// Live-only: the stored answer still has the text the caller parses.
	if res.Answer != strings.Join(chunks, "") {
		t.Fatalf("answer = %q, want the original pseudo-call", res.Answer)
	}
}

func Test_LPS03_FilterFlushesShortText(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hi"})
	cfg := Config{
		Provider: p, Model: "m", Clock: clock,
		TextFilter: func() StreamFilter { return &holdPrefixFilter{prefix: "<tool_call>"} },
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	if res.Answer != "hi" {
		t.Fatalf("answer = %q", res.Answer)
	}
	var shown string
	textAt, endAt := -1, -1
	for i, e := range events {
		switch e.Type {
		case proto.EventTextDelta:
			shown += e.Text
			if textAt < 0 {
				textAt = i
			}
		case proto.EventMessageEnd:
			endAt = i
		}
	}
	if shown != "hi" {
		t.Fatalf("shown = %q, want the flushed text", shown)
	}
	if textAt < 0 || endAt < 0 || textAt > endAt {
		t.Fatalf("flushed text was not emitted before message_end: %v", eventTypes(events))
	}
}

func Test_LPS04_ReasoningFilterIsIndependent(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Reasoning: "think", Text: "answer"})
	cfg := Config{
		Provider: p, Model: "m", Clock: clock,
		TextFilter:      func() StreamFilter { return dropFilter{} },
		ReasoningFilter: func() StreamFilter { return rewriteFilter{fn: strings.ToUpper} },
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	if got := countType(events, proto.EventTextDelta); got != 0 {
		t.Errorf("text_delta count = %d, want none", got)
	}
	var thinking string
	for _, e := range events {
		if e.Type == proto.EventThinkingDelta {
			thinking += e.Text
		}
	}
	if thinking != "THINK" {
		t.Errorf("thinking shown = %q, want the reasoning filter's rewrite", thinking)
	}
	if res.Answer != "answer" {
		t.Errorf("answer = %q, want the original text", res.Answer)
	}
	last := res.Messages[len(res.Messages)-1]
	if last.Reasoning != "think" {
		t.Errorf("stored reasoning = %q, want the original", last.Reasoning)
	}
}

func Test_LPS05_FilterPanicPassesThrough(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hello"})
	cfg := Config{
		Provider: p, Model: "m", Clock: clock,
		TextFilter: func() StreamFilter { return panicFilter{} },
	}

	_, events := runCollect(t, cfg, nil, message.User("go"))
	var shown string
	for _, e := range events {
		if e.Type == proto.EventTextDelta {
			shown += e.Text
		}
	}
	if shown != "hello" {
		t.Fatalf("shown = %q, want the unmodified text", shown)
	}
	found := false
	for _, e := range events {
		if e.Type == proto.EventExtensionError && e.Hook == "text_filter" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no text_filter extension_error: %v", eventTypes(events))
	}
}

func Test_LPS06_FilterFactoryPanicPassesThrough(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hello"})
	cfg := Config{
		Provider: p, Model: "m", Clock: clock,
		TextFilter: func() StreamFilter { panic("factory") },
	}

	_, events := runCollect(t, cfg, nil, message.User("go"))
	var shown string
	for _, e := range events {
		if e.Type == proto.EventTextDelta {
			shown += e.Text
		}
	}
	if shown != "hello" {
		t.Fatalf("shown = %q, want the unfiltered text", shown)
	}
	if got := countType(events, proto.EventExtensionError); got != 1 {
		t.Errorf("extension_error count = %d, want 1", got)
	}
}

func Test_LPS07_FilterIsFreshPerTurn(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	factories := 0
	p := testkit.NewProvider(clock, namedToolTurn("echo"), testkit.Turn{Text: "two"})
	cfg := Config{
		Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()),
		TextFilter: func() StreamFilter {
			factories++
			return dropFilter{}
		},
	}

	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Answer != "two" {
		t.Fatalf("answer = %q", res.Answer)
	}
	if factories != 2 {
		t.Fatalf("filter factories = %d, want one per turn", factories)
	}
}

func Test_LPS08_NoFilterIsUnchanged(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hello"})
	cfg := Config{Provider: p, Model: "m", Clock: clock}

	_, events := runCollect(t, cfg, nil, message.User("go"))
	testkit.Golden(t, "stream_filter_none", eventsJSON(t, events))
}

func Test_LPS09_LiveOnlyRewrite(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, testkit.Turn{Text: "hello"})
	cfg := Config{
		Provider: p, Model: "m", Clock: clock,
		TextFilter: func() StreamFilter { return rewriteFilter{fn: strings.ToUpper} },
	}

	res, events := runCollect(t, cfg, nil, message.User("go"))
	var shown string
	for _, e := range events {
		if e.Type == proto.EventTextDelta {
			shown += e.Text
		}
	}
	if shown != "HELLO" {
		t.Fatalf("shown = %q, want the rewrite", shown)
	}
	if res.Answer != "hello" {
		t.Fatalf("answer = %q, want the original", res.Answer)
	}
	last := res.Messages[len(res.Messages)-1]
	if last.Content != "hello" {
		t.Fatalf("stored content = %q, want the original", last.Content)
	}
}

// holdProvider emits one text delta and then blocks until the run is cancelled.
type holdProvider struct {
	emitted chan struct{}
}

func (p *holdProvider) Name() string { return "hold" }

func (p *holdProvider) Stream(ctx context.Context, _ provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: "held"})
	}
	close(p.emitted)
	<-ctx.Done()
	return nil, ctx.Err()
}

func Test_LPS10_CancellationMidHoldDoesNotLeak(t *testing.T) {
	testkit.NoLeaks(t)
	clock := testkit.NewFakeClock(baseTime)
	p := &holdProvider{emitted: make(chan struct{})}
	cfg := Config{
		Provider: p, Model: "m", Clock: clock,
		TextFilter: func() StreamFilter { return dropFilter{} },
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
