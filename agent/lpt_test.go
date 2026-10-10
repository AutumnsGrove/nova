package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// waitFor reads n signals, failing rather than blocking forever on a bug.
func waitFor(t *testing.T, ch <-chan struct{}, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for signal %d of %d", i+1, n)
		}
	}
}

// concur measures how many tool calls run at once.
type concur struct {
	mu     sync.Mutex
	active int
	peak   int
}

func (c *concur) enter() {
	c.mu.Lock()
	c.active++
	if c.active > c.peak {
		c.peak = c.active
	}
	c.mu.Unlock()
}

func (c *concur) exit() {
	c.mu.Lock()
	c.active--
	c.mu.Unlock()
}

func (c *concur) max() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peak
}

// gateTool enters, signals, waits for the gate, then exits.
func gateTool(name string, parallel bool, c *concur, entered chan<- struct{}, gate <-chan struct{}) tool.Tool {
	return toolOf(name, parallel, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		c.enter()
		entered <- struct{}{}
		<-gate
		c.exit()
		return tool.Text("ok"), nil
	})
}

func callN(name string, n int) []testkit.Call {
	calls := make([]testkit.Call, n)
	for i := range calls {
		calls[i] = testkit.Call{ID: fmt.Sprintf("%s_%d", name, i), Name: name, Args: `{}`}
	}
	return calls
}

func assertValid(t *testing.T, msgs []message.Message) {
	t.Helper()
	if err := message.Validate(msgs); err != nil {
		t.Fatalf("transcript invalid: %v", err)
	}
}

// runOutcome carries an asynchronous run's result across a goroutine.
type runOutcome struct {
	res *Result
	err error
}

func runAsync(cfg Config, history []message.Message, prompt message.Message) runOutcome {
	res, err := New(cfg).Run(context.Background(), history, prompt)
	return runOutcome{res, err}
}

func Test_LPT01_OneToolCall(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	toolIdx, assistantIdx := -1, -1
	for i, m := range res.Messages {
		if m.Role == message.RoleAssistant && len(m.ToolCalls) > 0 {
			assistantIdx = i
		}
		if m.Role == message.RoleTool {
			toolIdx = i
		}
	}
	if assistantIdx < 0 || toolIdx < assistantIdx {
		t.Fatalf("result appended before assistant: %+v", res.Messages)
	}
	assertValid(t, res.Messages)
}

func Test_LPT02_ManyParallelCalls(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	c := &concur{}
	entered := make(chan struct{}, 5)
	gate := make(chan struct{})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: callN("conc", 5)},
		testkit.Turn{Text: "done"},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(gateTool("conc", true, c, entered, gate))}
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	waitFor(t, entered, 5)
	close(gate)
	if out := <-done; out.err != nil {
		t.Fatalf("run: %v", out.err)
	}
	if c.max() < 2 {
		t.Fatalf("peak concurrency = %d, want > 1", c.max())
	}
}

func Test_LPT03_CompletionOrderDiffersFromCallOrder(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	release := make(chan struct{})
	fastRan := make(chan struct{})
	slow := toolOf("slow", true, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		<-release
		return tool.Text("slow"), nil
	})
	fast := toolOf("fast", true, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		close(fastRan)
		return tool.Text("fast"), nil
	})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "slow", `{}`), toolCall("c2", "fast", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(slow, fast)}
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	<-fastRan
	close(release)
	out := <-done
	if out.err != nil {
		t.Fatalf("run: %v", out.err)
	}
	res := out.res
	var got []string
	for _, m := range res.Messages {
		if m.Role == message.RoleTool {
			got = append(got, m.Content)
		}
	}
	if len(got) != 2 || got[0] != "slow" || got[1] != "fast" {
		t.Fatalf("tool results = %v, want [slow fast] in call order", got)
	}
	assertValid(t, res.Messages)
}

func Test_LPT04_SequentialMode(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	c := &concur{}
	gate := make(chan struct{})
	close(gate)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: callN("conc", 5)},
		testkit.Turn{Text: "done"},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, ToolExecution: Sequential, Tools: registry(gateTool("conc", true, c, make(chan struct{}, 5), gate))}
	runCollect(t, cfg, nil, message.User("go"))
	if c.max() != 1 {
		t.Fatalf("peak concurrency = %d, want 1", c.max())
	}
}

func Test_LPT05_NonParallelIsBarrier(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var mu sync.Mutex
	var log []string
	record := func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() }
	mk := func(name string, parallel bool) tool.Tool {
		return toolOf(name, parallel, func(_ context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
			var in struct {
				Tag string `json:"tag"`
			}
			_ = json.Unmarshal(args, &in)
			record("enter:" + in.Tag)
			record("exit:" + in.Tag)
			return tool.Text("ok"), nil
		})
	}
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{
			{ID: "c1", Name: "par", Args: `{"tag":"p1"}`},
			{ID: "c2", Name: "non", Args: `{"tag":"n"}`},
			{ID: "c3", Name: "par", Args: `{"tag":"p3"}`},
		}},
		testkit.Turn{Text: "done"},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(mk("par", true), mk("non", false))}
	runCollect(t, cfg, nil, message.User("go"))
	want := []string{"enter:p1", "exit:p1", "enter:n", "exit:n", "enter:p3", "exit:p3"}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(log) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", log, want)
	}
}

func Test_LPT06_MaxParallel(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	c := &concur{}
	entered := make(chan struct{}, 20)
	gate := make(chan struct{})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: callN("conc", 20)},
		testkit.Turn{Text: "done"},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, MaxParallel: 3, Tools: registry(gateTool("conc", true, c, entered, gate))}
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	waitFor(t, entered, 3)
	close(gate)
	if out := <-done; out.err != nil {
		t.Fatalf("run: %v", out.err)
	}
	if c.max() != 3 {
		t.Fatalf("peak concurrency = %d, want 3", c.max())
	}
}

func panicTool(name string) tool.Tool {
	return toolOf(name, true, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		panic("boom")
	})
}

func Test_LPT07_PanicParallel(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	ok := staticTool("ok", "fine")
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "boom", `{}`), toolCall("c2", "ok", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(panicTool("boom"), ok)}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Answer != "done" {
		t.Fatalf("answer = %q", res.Answer)
	}
	assertErrorResult(t, res.Messages, "boom")
	assertValid(t, res.Messages)
}

func Test_LPT08_PanicSequential(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "boom", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	cfg := Config{Provider: p, Model: "m", Clock: clock, ToolExecution: Sequential, Tools: registry(panicTool("boom"))}
	res, _ := runCollect(t, cfg, nil, message.User("go"))
	if res.Answer != "done" {
		t.Fatalf("answer = %q", res.Answer)
	}
	assertErrorResult(t, res.Messages, "boom")
}

func assertErrorResult(t *testing.T, msgs []message.Message, want string) {
	t.Helper()
	for _, m := range msgs {
		if m.Role == message.RoleTool && strings.Contains(m.Content, want) {
			return
		}
	}
	t.Fatalf("no error result mentioning %q in %+v", want, msgs)
}

func Test_LPT09_ToolReturnsError(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	failing := toolOf("fail", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Result{}, errors.New("nope")
	})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "fail", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(failing)}, nil, message.User("go"))
	assertErrorResult(t, res.Messages, "nope")
}

func Test_LPT10_EmptyResult(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	empty := toolOf("empty", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Result{}, nil
	})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "empty", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(empty)}, nil, message.User("go"))
	assertErrorResult(t, res.Messages, "(empty result)")
}

func Test_LPT11_EnormousContent(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 50 MiB")
	}
	clock := testkit.NewFakeClock(baseTime)
	big := make([]byte, 50<<20)
	for i := range big {
		big[i] = 'x'
	}
	huge := toolOf("huge", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Text("%s", string(big)), nil
	})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "huge", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(huge)}, nil, message.User("go"))
	found := false
	for _, m := range res.Messages {
		if m.Role == message.RoleTool && len(m.Content) == len(big) {
			found = true
		}
	}
	if !found {
		t.Fatal("enormous result not recorded intact")
	}
}

func Test_LPT12_EmptyCallID(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "", Name: "echo", Args: `{}`}}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	ids := toolResultIDs(res.Messages)
	if len(ids) != 1 || ids[0] != "call_0_0" {
		t.Fatalf("ids = %v, want [call_0_0]", ids)
	}
	assertValid(t, res.Messages)
}

func toolResultIDs(msgs []message.Message) []string {
	var ids []string
	for _, m := range msgs {
		if m.Role == message.RoleTool {
			ids = append(ids, m.ToolCallID)
		}
	}
	return ids
}

func Test_LPT13_DuplicateCallIDs(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{
			{ID: "x", Name: "echo", Args: `{}`},
			{ID: "x", Name: "echo", Args: `{}`},
		}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	ids := toolResultIDs(res.Messages)
	if len(ids) != 2 || ids[0] != "x" || ids[1] != "x_dup2" {
		t.Fatalf("ids = %v, want [x x_dup2]", ids)
	}
	assertValid(t, res.Messages)
}

func Test_LPT14_InvalidJSONArguments(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{"a":`)}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	assertErrorResult(t, res.Messages, "invalid")
	assertValid(t, res.Messages)
}

func Test_LPT15_NullAndEmptyArguments(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{
			{ID: "c1", Name: "echo", Args: ""},
			{ID: "c2", Name: "echo", Args: "null"},
		}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	for _, m := range res.Messages {
		if m.Role == message.RoleTool && m.Content != "echo:echoed" {
			t.Fatalf("result = %q, want echo:echoed", m.Content)
		}
	}
}

func Test_LPT16_UnknownToolListsKnown(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "ghost", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	for _, m := range res.Messages {
		if m.Role == message.RoleTool {
			if !strings.Contains(m.Content, "unknown tool") || !strings.Contains(m.Content, "echo") {
				t.Fatalf("unknown-tool result = %q", m.Content)
			}
		}
	}
}

func Test_LPT17_ToolCallAfterFinalAnswer(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock,
		testkit.Turn{Text: "interim", ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}},
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c2", "echo", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool())}, nil, message.User("go"))
	if res.Answer != "done" || res.Turns != 3 {
		t.Fatalf("result = %+v", res)
	}
	assertValid(t, res.Messages)
}

func Test_LPT18_ToolProgress(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	progressing := toolOf("prog", false, func(_ context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		tctx.Progress("one")
		tctx.Progress("two")
		tctx.Progress("three")
		return tool.Text("ok"), nil
	})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "prog", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	_, events := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(progressing)}, nil, message.User("go"))
	var got []string
	for _, e := range events {
		if e.Type == proto.EventToolUpdate {
			got = append(got, e.Text)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{"one", "two", "three"}) {
		t.Fatalf("tool_update = %v", got)
	}
}

func Test_LPT19_SlowProgressConsumer(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var returned sync.WaitGroup
	returned.Add(1)
	progressing := toolOf("prog", false, func(_ context.Context, _ json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		for i := 0; i < 100; i++ {
			tctx.Progress(fmt.Sprintf("u%d", i))
		}
		returned.Done()
		return tool.Text("final"), nil
	})
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{toolCall("c1", "prog", `{}`)}},
		testkit.Turn{Text: "done"},
	)
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	c := &collector{}
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(progressing), Emit: func(e proto.Event) {
		if e.Type == proto.EventToolUpdate {
			once.Do(func() { close(blocked) })
			<-release
		}
		c.emit(e)
	}}
	done := make(chan struct{})
	var res *Result
	go func() {
		res, _ = New(cfg).Run(context.Background(), nil, message.User("go"))
		close(done)
	}()
	<-blocked
	returned.Wait() // the tool finished despite the blocked consumer
	close(release)
	<-done
	if res.Answer != "done" {
		t.Fatalf("answer = %q", res.Answer)
	}
	if got := c.count(proto.EventToolUpdate); got == 0 || got > 100 {
		t.Fatalf("tool_update count = %d", got)
	}
}

func Test_LPT20_TranscriptValidAfterEachCase(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	cases := []struct {
		name  string
		turns []testkit.Turn
		tools []tool.Tool
	}{
		{"ok", []testkit.Turn{{ToolCalls: []testkit.Call{toolCall("c1", "echo", `{}`)}}, {Text: "done"}}, []tool.Tool{echoTool()}},
		{"panic", []testkit.Turn{{ToolCalls: []testkit.Call{toolCall("c1", "boom", `{}`)}}, {Text: "done"}}, []tool.Tool{panicTool("boom")}},
		{"dup", []testkit.Turn{{ToolCalls: []testkit.Call{{ID: "x", Name: "echo", Args: `{}`}, {ID: "x", Name: "echo", Args: `{}`}}}, {Text: "done"}}, []tool.Tool{echoTool()}},
		{"unknown", []testkit.Turn{{ToolCalls: []testkit.Call{toolCall("c1", "ghost", `{}`)}}, {Text: "done"}}, []tool.Tool{echoTool()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testkit.NewProvider(clock, tc.turns...)
			res, _ := runCollect(t, Config{Provider: p, Model: "m", Clock: clock, Tools: registry(tc.tools...)}, nil, message.User("go"))
			assertValid(t, res.Messages)
		})
	}
}
