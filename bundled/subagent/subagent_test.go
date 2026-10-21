package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
	"github.com/AutumnsGrove/nova/usage"
)

// fakeRegistry reports tool existence.
type fakeRegistry struct{ names []string }

func (r fakeRegistry) Get(name string) (tool.Tool, bool) {
	for _, n := range r.names {
		if n == name {
			return nil, true
		}
	}
	return nil, false
}

func defaultRegistry() fakeRegistry {
	return fakeRegistry{names: []string{"read", "list", "glob", "grep", "web_fetch", "bash", "subagent"}}
}

// fakeSpawn records requests and plays a scripted function.
type fakeSpawn struct {
	mu   sync.Mutex
	reqs []ext.SpawnRequest
	fn   func(ctx context.Context, r ext.SpawnRequest) (*ext.SpawnResult, error)
}

func (f *fakeSpawn) spawn(ctx context.Context, r ext.SpawnRequest) (*ext.SpawnResult, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(ctx, r)
	}
	return &ext.SpawnResult{Answer: "done", Turns: 1, Reason: "done"}, nil
}

func (f *fakeSpawn) last() ext.SpawnRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqs[len(f.reqs)-1]
}

func setup(reg toolRegistry, cfg *config.Config, spawn func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error)) *Extension {
	e := New()
	env := &ext.Env{
		Config: cfg,
		Extra:  map[string]any{"tools": reg},
		Spawn:  spawn,
		Tool:   func(tool.Tool) error { return nil },
	}
	_ = e.Init(env)
	return e
}

// progress collects tool updates.
type progress struct {
	mu   sync.Mutex
	msgs []string
}

func (p *progress) emit(u tool.Update) {
	p.mu.Lock()
	p.msgs = append(p.msgs, u.Text)
	p.mu.Unlock()
}

func (p *progress) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.msgs...)
}

func TestSubagent_SUB01_BasicRun(t *testing.T) {
	fake := &fakeSpawn{fn: func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error) {
		return &ext.SpawnResult{Answer: "the answer", Turns: 3, Reason: "done", Tools: []string{"read"}}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	res, err := e.run(context.Background(), json.RawMessage(`{"task":"do it"}`), nil)
	if err != nil || res.Content != "the answer" {
		t.Fatalf("result = %+v err=%v", res, err)
	}
}

func TestSubagent_SUB02_DefaultTools(t *testing.T) {
	fake := &fakeSpawn{}
	e := setup(defaultRegistry(), nil, fake.spawn)
	_, _ = e.run(context.Background(), json.RawMessage(`{"task":"do it"}`), nil)
	got := strings.Join(fake.last().Tools, ",")
	want := "read,list,glob,grep,web_fetch"
	if got != want {
		t.Fatalf("tools = %q, want %q", got, want)
	}
}

func TestSubagent_SUB03_RequestedTools(t *testing.T) {
	fake := &fakeSpawn{}
	e := setup(defaultRegistry(), nil, fake.spawn)
	_, _ = e.run(context.Background(), json.RawMessage(`{"task":"do it","tools":["read","bash"]}`), nil)
	got := strings.Join(fake.last().Tools, ",")
	if got != "read,bash" {
		t.Fatalf("tools = %q", got)
	}
}

func TestSubagent_SUB04_UnknownTool(t *testing.T) {
	e := setup(defaultRegistry(), nil, (&fakeSpawn{}).spawn)
	res, _ := e.run(context.Background(), json.RawMessage(`{"task":"do it","tools":["ghost"]}`), nil)
	if !res.IsError || !strings.Contains(res.Content, "ghost") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestSubagent_SUB05_NotOfferedToChild(t *testing.T) {
	e := setup(defaultRegistry(), nil, (&fakeSpawn{}).spawn)
	tctx := &tool.Context{Data: Depth{N: 1}}
	res, _ := e.run(context.Background(), json.RawMessage(`{"task":"do it","tools":["subagent"]}`), tctx)
	if !res.IsError || !strings.Contains(res.Content, "not available") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestSubagent_SUB06_DepthLimit(t *testing.T) {
	cfg := &config.Config{Subagent: config.SubagentConfig{MaxDepth: 2}}
	fake := &fakeSpawn{}
	e := setup(defaultRegistry(), cfg, fake.spawn)
	if _, err := e.resolveTools([]string{"subagent"}, 1); err != nil {
		t.Fatalf("depth 1 should allow subagent: %v", err)
	}
	if _, err := e.resolveTools([]string{"subagent"}, 2); err == nil {
		t.Fatal("depth 2 should refuse subagent")
	}
}

// SUB07: max_turns is optional, with no default and no cap; an unset value is
// passed through as 0 (unlimited) and a caller's value is passed unchanged.
func TestSubagent_SUB07_MaxTurns(t *testing.T) {
	fake := &fakeSpawn{}
	e := setup(defaultRegistry(), nil, fake.spawn)
	_, _ = e.run(context.Background(), json.RawMessage(`{"task":"a"}`), nil)
	if got := fake.last().MaxTurns; got != 0 {
		t.Fatalf("default max_turns = %d, want 0 (unlimited)", got)
	}
	_, _ = e.run(context.Background(), json.RawMessage(`{"task":"b","max_turns":500}`), nil)
	if got := fake.last().MaxTurns; got != 500 {
		t.Fatalf("max_turns = %d, want 500 (no cap)", got)
	}
}

func TestSubagent_SUB08_Exhausted(t *testing.T) {
	fake := &fakeSpawn{fn: func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error) {
		return &ext.SpawnResult{Answer: "partial", Turns: 15, Reason: "exhausted"}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	res, _ := e.run(context.Background(), json.RawMessage(`{"task":"do it"}`), nil)
	if !strings.Contains(res.Content, "partial") || !strings.Contains(res.Content, "exhausted") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestSubagent_SUB09_ProviderError(t *testing.T) {
	fake := &fakeSpawn{fn: func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error) {
		return nil, context.DeadlineExceeded
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	res, _ := e.run(context.Background(), json.RawMessage(`{"task":"do it"}`), nil)
	if !res.IsError {
		t.Fatalf("result = %+v", res)
	}
}

func TestSubagent_SUB10_PolicyInherited(t *testing.T) {
	reg := tool.NewRegistry()
	reg.MustRegister(tool.New(tool.Info{Name: "bash"}, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Text("ran"), nil
	}))
	prov := testkit.NewProvider(testkit.NewFakeClock(time.Unix(0, 0)),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "bash", Args: "{}"}}},
		testkit.Turn{Text: "final"},
	)
	var mu sync.Mutex
	var decisions []string
	sp := &Spawner{
		Provider: prov, Model: "m", Registry: reg,
		Policy: &permission.Policy{Rules: []permission.Rule{{Tool: "bash", Action: permission.Deny}}},
		Clock:  testkit.NewFakeClock(time.Unix(0, 0)),
		Emit: func(ev proto.Event) {
			if ev.Type == proto.EventPermission {
				mu.Lock()
				decisions = append(decisions, ev.Decision)
				mu.Unlock()
			}
		},
	}
	res, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t", Tools: []string{"bash"}, MaxTurns: 3})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(decisions) == 0 || decisions[0] != "deny" {
		t.Fatalf("decisions = %v, want deny", decisions)
	}
	_ = res
}

func TestSubagent_SUB11_ApprovalPath(t *testing.T) {
	reg := tool.NewRegistry()
	reg.MustRegister(tool.New(tool.Info{Name: "bash"}, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Text("ran"), nil
	}))
	prov := testkit.NewProvider(testkit.NewFakeClock(time.Unix(0, 0)),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "bash", Args: "{}"}}},
		testkit.Turn{Text: "final"},
	)
	var mu sync.Mutex
	var reasons []string
	sp := &Spawner{
		Provider: prov, Model: "m", Registry: reg,
		Policy: &permission.Policy{Default: permission.Ask},
		Ask: func(_ context.Context, q agent.Question) (bool, error) {
			mu.Lock()
			reasons = append(reasons, q.Reason)
			mu.Unlock()
			return true, nil
		},
		Clock: testkit.NewFakeClock(time.Unix(0, 0)),
	}
	if _, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t", Tools: []string{"bash"}, MaxTurns: 3}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reasons) == 0 || !strings.HasPrefix(reasons[0], "[subagent] ") {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestSubagent_SUB12_ParentHookGuardsChild(t *testing.T) {
	reg := tool.NewRegistry()
	reg.MustRegister(tool.New(tool.Info{Name: "bash"}, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Text("ran"), nil
	}))
	prov := testkit.NewProvider(testkit.NewFakeClock(time.Unix(0, 0)),
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "1", Name: "bash", Args: "{}"}}},
		testkit.Turn{Text: "final"},
	)
	var blocked bool
	sp := &Spawner{
		Provider: prov, Model: "m", Registry: reg,
		ToolCall: func(context.Context, agent.ToolCall) *agent.CallDecision {
			blocked = true
			return &agent.CallDecision{Block: true, Reason: "blocked by parent"}
		},
		Clock: testkit.NewFakeClock(time.Unix(0, 0)),
	}
	if _, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t", Tools: []string{"bash"}, MaxTurns: 3}); err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("the parent tool_call hook did not guard the child")
	}
}

func TestSubagent_SUB13_OtherHooksNotApplied(t *testing.T) {
	// The Spawner wires only ToolCall and ToolResult; there is no field for any
	// other parent hook, so a parent TurnEnd/MessageEnd cannot run for a child.
	prov := testkit.NewProvider(testkit.NewFakeClock(time.Unix(0, 0)), testkit.Turn{Text: "final"})
	sp := &Spawner{Provider: prov, Model: "m", Registry: tool.NewRegistry(), Clock: testkit.NewFakeClock(time.Unix(0, 0))}
	if _, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t", MaxTurns: 2}); err != nil {
		t.Fatal(err)
	}
}

func TestSubagent_SUB14_ConcurrencyCap(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	active, peak := 0, 0
	fake := &fakeSpawn{fn: func(ctx context.Context, _ ext.SpawnRequest) (*ext.SpawnResult, error) {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		<-release
		mu.Lock()
		active--
		mu.Unlock()
		return &ext.SpawnResult{Answer: "ok", Reason: "done"}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.run(context.Background(), json.RawMessage(`{"task":"t"}`), nil)
		}()
	}
	// Wait until four are running, then let them all through.
	for {
		mu.Lock()
		n := active
		mu.Unlock()
		if n >= 4 {
			break
		}
	}
	close(release)
	wg.Wait()
	if peak > 4 {
		t.Fatalf("peak concurrency = %d, want at most 4", peak)
	}
}

func TestSubagent_SUB15_Cancel(t *testing.T) {
	started := make(chan struct{})
	fake := &fakeSpawn{fn: func(ctx context.Context, _ ext.SpawnRequest) (*ext.SpawnResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan tool.Result, 1)
	go func() {
		res, _ := e.run(ctx, json.RawMessage(`{"task":"t"}`), nil)
		done <- res
	}()
	<-started
	cancel()
	select {
	case res := <-done:
		if !res.IsError || !strings.Contains(res.Content, "cancelled") {
			t.Fatalf("result = %q", res.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func TestSubagent_SUB16_ProgressLines(t *testing.T) {
	fake := &fakeSpawn{fn: func(_ context.Context, r ext.SpawnRequest) (*ext.SpawnResult, error) {
		for _, name := range []string{"read", "grep", "bash"} {
			r.OnEvent(proto.Event{Type: proto.EventToolCall, Call: &proto.ToolCall{Name: name}})
		}
		return &ext.SpawnResult{Answer: "ok", Turns: 3, Reason: "done"}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	p := &progress{}
	tctx := &tool.Context{Emit: p.emit}
	_, _ = e.run(context.Background(), json.RawMessage(`{"task":"t"}`), tctx)
	msgs := p.all()
	bullets := 0
	for _, m := range msgs {
		if strings.HasPrefix(m, "▸ ") {
			bullets++
		}
	}
	if bullets != 3 || !strings.Contains(strings.Join(msgs, "\n"), "3 turn") {
		t.Fatalf("updates = %v", msgs)
	}
}

func TestSubagent_SUB17_ReasoningNotForwarded(t *testing.T) {
	fake := &fakeSpawn{fn: func(_ context.Context, r ext.SpawnRequest) (*ext.SpawnResult, error) {
		r.OnEvent(proto.Event{Type: proto.EventThinkingDelta, Text: "secret"})
		return &ext.SpawnResult{Answer: "ok", Reason: "done"}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	p := &progress{}
	tctx := &tool.Context{Emit: p.emit}
	_, _ = e.run(context.Background(), json.RawMessage(`{"task":"t"}`), tctx)
	for _, m := range p.all() {
		if strings.Contains(m, "secret") {
			t.Fatalf("reasoning forwarded: %v", p.all())
		}
	}
}

func TestSubagent_SUB18_ResultCap(t *testing.T) {
	big := strings.Repeat("x", 100*1024)
	fake := &fakeSpawn{fn: func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error) {
		return &ext.SpawnResult{Answer: big, Reason: "done"}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	res, _ := e.run(context.Background(), json.RawMessage(`{"task":"t"}`), nil)
	if len(res.Content) != resultCap || !res.Truncated {
		t.Fatalf("content len = %d truncated = %v", len(res.Content), res.Truncated)
	}
}

func TestSubagent_SUB19_NestedUsage(t *testing.T) {
	u := provider.Usage{PromptTokens: 100, CompletionTokens: 20}
	fake := &fakeSpawn{fn: func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error) {
		return &ext.SpawnResult{Answer: "ok", Reason: "done", Usage: u}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	res, _ := e.run(context.Background(), json.RawMessage(`{"task":"t"}`), nil)
	var details map[string]any
	_ = json.Unmarshal(res.Details, &details)
	if _, ok := details["nested_usage"]; !ok {
		t.Fatalf("details = %v", details)
	}
	// The Spawner adds the child usage to the ledger when one is attached.
	ledger := usage.NewLedger(nil)
	sp := &Spawner{Provider: scriptedAnswer(u), Model: "m", Registry: tool.NewRegistry(), Ledger: ledger, Clock: testkit.NewFakeClock(time.Unix(0, 0))}
	if _, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t", MaxTurns: 2}); err != nil {
		t.Fatal(err)
	}
	if ledger.Total().PromptTokens != 100 {
		t.Fatalf("ledger total = %+v", ledger.Total())
	}
}

// scriptedAnswer is a provider that returns one answer with the given usage.
func scriptedAnswer(u provider.Usage) provider.Provider {
	return testkit.NewProvider(testkit.NewFakeClock(time.Unix(0, 0)), testkit.Turn{Text: "ok", Usage: &u})
}

func TestSubagent_SUB20_Details(t *testing.T) {
	fake := &fakeSpawn{fn: func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error) {
		return &ext.SpawnResult{Answer: "ok", Turns: 4, Reason: "done", Tools: []string{"read"}, Usage: provider.Usage{PromptTokens: 5}}, nil
	}}
	e := setup(defaultRegistry(), nil, fake.spawn)
	res, _ := e.run(context.Background(), json.RawMessage(`{"task":"t"}`), nil)
	var details map[string]json.RawMessage
	_ = json.Unmarshal(res.Details, &details)
	for _, key := range []string{"turns", "tools", "duration_ms", "usage", "reason"} {
		if _, ok := details[key]; !ok {
			t.Fatalf("details missing %s: %s", key, res.Details)
		}
	}
}

func TestSubagent_SUB21_ModelOverride(t *testing.T) {
	fake := &fakeSpawn{}
	e := setup(defaultRegistry(), nil, fake.spawn)
	_, _ = e.run(context.Background(), json.RawMessage(`{"task":"t","model":"special"}`), nil)
	if fake.last().Model != "special" {
		t.Fatalf("model = %q", fake.last().Model)
	}
}

func TestSubagent_SUB22_PanicInChild(t *testing.T) {
	fake := &fakeSpawn{fn: func(context.Context, ext.SpawnRequest) (*ext.SpawnResult, error) {
		panic("child exploded")
	}}
	h := ext.NewHost(ext.HostOptions{
		Registry: tool.NewRegistry(),
		Session:  noopSession{},
		Emit:     func(proto.Event) {},
	})
	_ = h.AddBundled(setup(defaultRegistry(), nil, fake.spawn))
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("start: %v", errs)
	}
	res := h.Registry().Execute(context.Background(), "subagent", json.RawMessage(`{"task":"t"}`), nil)
	if !res.IsError {
		t.Fatalf("result = %+v, want an error", res)
	}
}

// noopSession is an empty session for host tests.
type noopSession struct{}

func (noopSession) AppendEntry(string, json.RawMessage, bool) error { return nil }
func (noopSession) Entries(string) []json.RawMessage                { return nil }

func TestSubagent_SUB23_NoUI(t *testing.T) {
	fake := &fakeSpawn{}
	e := setup(defaultRegistry(), nil, fake.spawn)
	res, err := e.run(context.Background(), json.RawMessage(`{"task":"t"}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("result = %+v err=%v", res, err)
	}
}

// TestSubagent_SUB31_MaxTokensInherited checks the parent's output cap reaches
// the child's requests, so a sub-agent cannot spend more per call than the
// session allows.
func TestSubagent_SUB31_MaxTokensInherited(t *testing.T) {
	prov := testkit.NewProvider(testkit.NewFakeClock(time.Unix(0, 0)), testkit.Turn{Text: "final"})
	sp := &Spawner{
		Provider: prov, Model: "m", Registry: tool.NewRegistry(),
		MaxTokens: 24, Clock: testkit.NewFakeClock(time.Unix(0, 0)),
	}
	if _, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t"}); err != nil {
		t.Fatal(err)
	}
	if got := prov.Requests()[0].MaxTokens; got != 24 {
		t.Fatalf("child request MaxTokens = %d, want 24", got)
	}
}

// TestSubagent_SUB32_ChildKeepsRouting checks the child's requests carry the
// parent's catalog id, so the router sends them to the session's provider
// instead of falling back to the default one.
func TestSubagent_SUB32_ChildKeepsRouting(t *testing.T) {
	prov := testkit.NewProvider(testkit.NewFakeClock(time.Unix(0, 0)), testkit.Turn{Text: "final"})
	sp := &Spawner{
		Provider: prov, Model: "wire-slug", ModelID: "provider/catalog-id",
		Registry: tool.NewRegistry(), Clock: testkit.NewFakeClock(time.Unix(0, 0)),
	}
	if _, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t"}); err != nil {
		t.Fatal(err)
	}
	req := prov.Requests()[0]
	if req.ModelID != "provider/catalog-id" || req.Model != "wire-slug" {
		t.Fatalf("child request = %+v, want the parent's catalog id and slug", req)
	}
}

// TestSubagent_SUB33_ModelOverrideUsesCatalog checks a tool that names another
// catalog model re-routes the child to that model rather than the parent's.
func TestSubagent_SUB33_ModelOverrideUsesCatalog(t *testing.T) {
	provider.RegisterModel(provider.Model{ID: "sub32-vision", Slug: "vision-slug", Provider: "test"})
	prov := testkit.NewProvider(testkit.NewFakeClock(time.Unix(0, 0)), testkit.Turn{Text: "final"})
	sp := &Spawner{
		Provider: prov, Model: "wire-slug", ModelID: "provider/catalog-id",
		Registry: tool.NewRegistry(), Clock: testkit.NewFakeClock(time.Unix(0, 0)),
	}
	if _, err := sp.Spawn(context.Background(), ext.SpawnRequest{Task: "t", Model: "sub32-vision"}); err != nil {
		t.Fatal(err)
	}
	req := prov.Requests()[0]
	if req.ModelID != "sub32-vision" || req.Model != "vision-slug" {
		t.Fatalf("child request = %+v, want the override's catalog id and slug", req)
	}
}
