package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// propTurn is one scripted response for the property provider.
type propTurn struct {
	text  string
	calls []message.ToolCall
}

// propProvider plays propTurns and can cancel the run at a chosen turn.
type propProvider struct {
	clock    *testkit.FakeClock
	turns    []propTurn
	cancelAt int
	cancel   context.CancelFunc
	i        int
}

func (p *propProvider) Name() string { return "prop" }

func (p *propProvider) Stream(ctx context.Context, _ provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	if p.i >= len(p.turns) {
		return nil, errors.New("prop: exhausted")
	}
	if p.cancelAt == p.i {
		p.i++
		if p.cancel != nil {
			p.cancel()
		}
		return nil, ctx.Err()
	}
	turn := p.turns[p.i]
	p.i++
	if turn.text != "" {
		onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: turn.text})
	}
	for i := range turn.calls {
		onEvent(provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &turn.calls[i]})
	}
	return &provider.Response{Message: message.Message{
		Role:      message.RoleAssistant,
		Content:   turn.text,
		ToolCalls: turn.calls,
	}}, nil
}

// propertyTools is the fixed tool set every random script draws on.
func propertyTools() []tool.Tool {
	return []tool.Tool{
		toolOf("ok", true, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			return tool.Text("ok"), nil
		}),
		toolOf("panic", true, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			panic("boom")
		}),
		toolOf("empty", true, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			return tool.Result{}, nil
		}),
		toolOf("fail", true, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			return tool.Result{}, errors.New("failed")
		}),
	}
}

var propertyNames = []string{"ok", "panic", "empty", "fail", "ghost"}
var propertyIDs = []string{"", "a", "b", "a", "call_0_0"}

// randomScript builds a turn script whose last turn always has text, so the run
// usually ends on its own.
func randomScript(rng *rand.Rand) []propTurn {
	n := 1 + rng.Intn(4)
	turns := make([]propTurn, n)
	for i := range turns {
		if i == n-1 || rng.Intn(3) == 0 {
			turns[i] = propTurn{text: fmt.Sprintf("t%d", i)}
			continue
		}
		k := 1 + rng.Intn(3)
		for j := 0; j < k; j++ {
			turns[i].calls = append(turns[i].calls, message.ToolCall{
				ID:   propertyIDs[rng.Intn(len(propertyIDs))],
				Type: "function",
				Function: message.FunctionCall{
					Name:      propertyNames[rng.Intn(len(propertyNames))],
					Arguments: `{}`,
				},
			})
		}
	}
	return turns
}

// runPropertyScript runs one random script and returns the result and events.
func runPropertyScript(t *testing.T, rng *rand.Rand, seed int64, iter int) (*Result, []proto.Event) {
	t.Helper()
	turns := randomScript(rng)
	cancelAt := -1
	if rng.Intn(4) == 0 {
		cancelAt = rng.Intn(len(turns))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &propProvider{clock: testkit.NewFakeClock(baseTime), turns: turns, cancelAt: cancelAt, cancel: cancel}
	c := &collector{}
	cfg := Config{Provider: p, Model: "m", Clock: testkit.NewFakeClock(baseTime), Tools: registry(propertyTools()...), Emit: c.emit}
	res, err := New(cfg).Run(ctx, nil, message.User("go"))
	if res == nil {
		t.Fatalf("seed %d iter %d: nil result (err %v)", seed, iter, err)
	}
	return res, c.all()
}

func TestProperty_LP_TranscriptAlwaysValid(t *testing.T) {
	seed := int64(20260102)
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 200; i++ {
		res, events := runPropertyScript(t, rng, seed, i)
		if err := message.Validate(res.Messages); err != nil {
			t.Fatalf("seed %d iter %d: invalid transcript: %v\n%+v", seed, i, err, res.Messages)
		}
		assertRunBoundaries(t, events)
		if countType(events, proto.EventRunStart) != 1 || countType(events, proto.EventRunEnd) != 1 {
			t.Fatalf("seed %d iter %d: run boundaries wrong: %v", seed, i, eventTypes(events))
		}
	}
}

func TestProperty_LP_NoEventAfterEnd(t *testing.T) {
	seed := int64(7)
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 200; i++ {
		_, events := runPropertyScript(t, rng, seed, i)
		for j, e := range events {
			if e.Type == proto.EventRunEnd && j != len(events)-1 {
				t.Fatalf("seed %d iter %d: event %s after run_end", seed, i, events[j+1].Type)
			}
		}
	}
}

func TestProperty_LP_Order(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	const n = 6
	gates := make([]chan struct{}, n)
	entered := make(chan struct{}, n)
	order := toolOf("order", true, func(_ context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
		var in struct {
			I int `json:"i"`
		}
		_ = json.Unmarshal(args, &in)
		entered <- struct{}{}
		<-gates[in.I]
		return tool.Text("r%d", in.I), nil
	})
	var calls []testkit.Call
	for i := 0; i < n; i++ {
		gates[i] = make(chan struct{})
		calls = append(calls, testkit.Call{ID: fmt.Sprintf("c%d", i), Name: "order", Args: fmt.Sprintf(`{"i":%d}`, i)})
	}
	p := testkit.NewProvider(clock, testkit.Turn{ToolCalls: calls}, textTurn("done"))
	cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(order)}
	done := make(chan runOutcome, 1)
	go func() { done <- runAsync(cfg, nil, message.User("go")) }()
	waitFor(t, entered, n)
	release := rand.New(rand.NewSource(99)).Perm(n)
	for _, i := range release {
		close(gates[i])
	}
	out := <-done
	if out.err != nil {
		t.Fatalf("run: %v", out.err)
	}
	var got []string
	for _, m := range out.res.Messages {
		if m.Role == message.RoleTool {
			got = append(got, m.Content)
		}
	}
	for i := range got {
		want := fmt.Sprintf("r%d", i)
		if got[i] != want {
			t.Fatalf("results = %v, want call order", got)
		}
	}
}

func FuzzToolCallAssembly(f *testing.F) {
	f.Add("id", "echo", `{}`)
	f.Add("", "", `{"a":`)
	f.Add("x", "ghost", "null")
	f.Fuzz(func(t *testing.T, id, name, args string) {
		clock := testkit.NewFakeClock(baseTime)
		p := testkit.NewProvider(clock,
			testkit.Turn{ToolCalls: []testkit.Call{{ID: id, Name: name, Args: args}}},
			textTurn("done"),
		)
		c := &collector{}
		cfg := Config{Provider: p, Model: "m", Clock: clock, Tools: registry(echoTool()), Emit: c.emit}
		res, err := New(cfg).Run(context.Background(), nil, message.User("go"))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if err := message.Validate(res.Messages); err != nil {
			t.Fatalf("invalid transcript: %v\n%+v", err, res.Messages)
		}
		assertRunBoundaries(t, c.all())
	})
}
