package ext

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/decide"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// eventSink collects emitted events for assertions.
type eventSink struct {
	mu     sync.Mutex
	events []proto.Event
}

func (s *eventSink) emit(ev proto.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *eventSink) countType(typ string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func (s *eventSink) countHook(hook string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.events {
		if ev.Type == proto.EventExtensionError && ev.Hook == hook {
			n++
		}
	}
	return n
}

// funcBundled is a Bundled backed by a function, for tests.
type funcBundled struct {
	name string
	init func(env *Env) error
}

func (f *funcBundled) Name() string { return f.name }

func (f *funcBundled) Init(env *Env) error {
	if f.init == nil {
		return nil
	}
	return f.init(env)
}

// textTool returns a trivial tool with the given name.
func textTool(name, answer string, info tool.Info) tool.Tool {
	info.Name = name
	if info.Description == "" {
		info.Description = name
	}
	return tool.New(info, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Text("%s", answer), nil
	})
}

func TestInProc_IPC01_RegisterToolCommandHooks(t *testing.T) {
	sink := &eventSink{}
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: sink.emit, Clock: testkit.NewFakeClock(time.Unix(0, 0))})
	err := h.AddBundled(&funcBundled{name: "one", init: func(env *Env) error {
		if err := env.Tool(textTool("bundled_tool", "ok", tool.Info{ReadOnly: true})); err != nil {
			return err
		}
		return env.Command(InProcCommand{Name: "bundled_cmd", Description: "does a thing", Run: func(_ context.Context, args string) (string, error) {
			return "ran " + args, nil
		}})
	}})
	if err != nil {
		t.Fatal(err)
	}
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	if _, ok := h.Registry().Get("bundled_tool"); !ok {
		t.Fatal("bundled tool not registered")
	}
	cmds := h.Commands()
	if len(cmds) != 1 || cmds[0].Name != "bundled_cmd" || cmds[0].Extension != "one" {
		t.Fatalf("commands = %+v", cmds)
	}
	out, err := h.RunCommand(context.Background(), "bundled_cmd", "x")
	if err != nil || out != "ran x" {
		t.Fatalf("RunCommand = %q, %v", out, err)
	}

	// Hooks are composed into the host's hook set.
	var ran bool
	h2 := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: sink.emit})
	_ = h2.AddBundled(&funcBundled{name: "hooks", init: func(env *Env) error {
		env.Hooks(agent.Hooks{ToolCall: func(context.Context, agent.ToolCall) *agent.CallDecision {
			ran = true
			return nil
		}})
		return nil
	}})
	if errs := h2.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	if h2.Hooks().ToolCall == nil {
		t.Fatal("ToolCall hook not composed")
	}
	_ = h2.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x"})
	if !ran {
		t.Fatal("bundled hook did not run")
	}
}

func TestInProc_IPC01b_Decide(t *testing.T) {
	var got decide.Request
	h := NewHost(HostOptions{
		Registry: tool.NewRegistry(),
		Session:  &fakeSession{},
		Decide: func(_ context.Context, req decide.Request) (*decide.Response, error) {
			got = req
			return &decide.Response{Answers: map[string]decide.Answer{"q": {Kind: decide.KindNoul, Noul: 0.9}}, Model: "build"}, nil
		},
	})
	err := h.AddBundled(&funcBundled{name: "one", init: func(env *Env) error {
		if env.Decide == nil {
			t.Fatal("Env.Decide is nil")
		}
		resp, err := env.Decide(context.Background(), decide.Request{
			Model:     "x",
			State:     "s",
			Questions: map[string]decide.Question{"q": {Kind: decide.KindNoul}},
		})
		if err != nil {
			return err
		}
		if resp.Answers["q"].Noul != 0.9 || resp.Model != "build" {
			t.Errorf("response = %+v", resp)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	if got.Model != "x" {
		t.Fatalf("decider request = %+v", got)
	}
}

func TestInProc_IPC02_NameClashWithSubprocess(t *testing.T) {
	t.Setenv("ECHO_TOOL_NAMES", "todo")
	dir := echoDir(t, "thirdparty", "")
	sink := &eventSink{}
	h := NewHost(HostOptions{Dirs: []string{dir}, Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: sink.emit, Clock: testkit.NewFakeClock(time.Unix(0, 0))})
	_ = h.AddBundled(&funcBundled{name: "tasks", init: func(env *Env) error {
		return env.Tool(textTool("todo", "bundled wins", tool.Info{ReadOnly: false}))
	}})
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	got := h.Registry().Execute(context.Background(), "todo", nil, nil)
	if got.Content != "bundled wins" {
		t.Fatalf("todo result = %q, want the bundled tool", got.Content)
	}
}

func TestInProc_IPC03_PanicInInitDisabled(t *testing.T) {
	sink := &eventSink{}
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: sink.emit, Clock: testkit.NewFakeClock(time.Unix(0, 0))})
	_ = h.AddBundled(&funcBundled{name: "good", init: func(env *Env) error {
		return env.Tool(textTool("good_tool", "ok", tool.Info{ReadOnly: true}))
	}})
	_ = h.AddBundled(&funcBundled{name: "bad", init: func(env *Env) error {
		panic("init exploded")
	}})
	errs := h.Start(context.Background())
	if len(errs) != 1 {
		t.Fatalf("Start errors = %v, want one", errs)
	}
	if _, ok := h.Registry().Get("good_tool"); !ok {
		t.Fatal("the other extension was affected")
	}
	for _, st := range h.Status() {
		if st.Name == "bad" && st.State != stateDisabled {
			t.Fatalf("bad state = %s, want disabled", st.State)
		}
	}
	if sink.countType(proto.EventExtensionError) == 0 {
		t.Fatal("no extension_error emitted")
	}
}

func TestInProc_IPC04_PanicInToolCommandHook(t *testing.T) {
	sink := &eventSink{}
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: sink.emit, Clock: testkit.NewFakeClock(time.Unix(0, 0))})
	_ = h.AddBundled(&funcBundled{name: "panicky", init: func(env *Env) error {
		if err := env.Tool(tool.New(tool.Info{Name: "boom"}, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			panic("tool exploded")
		})); err != nil {
			return err
		}
		if err := env.Command(InProcCommand{Name: "boomcmd", Run: func(context.Context, string) (string, error) {
			panic("command exploded")
		}}); err != nil {
			return err
		}
		env.Hooks(agent.Hooks{ToolCall: func(context.Context, agent.ToolCall) *agent.CallDecision {
			panic("hook exploded")
		}})
		return nil
	}})
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	got := h.Registry().Execute(context.Background(), "boom", nil, nil)
	if !got.IsError || !strings.Contains(got.Content, "internal error") {
		t.Fatalf("panicking tool result = %+v", got)
	}
	if _, err := h.RunCommand(context.Background(), "boomcmd", ""); err == nil {
		t.Fatal("panicking command did not error")
	}
	if dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x"}); dec != nil {
		t.Fatalf("panicking hook returned %+v", dec)
	}
	if sink.countHook("tool:boom") == 0 || sink.countHook("command:boomcmd") == 0 || sink.countHook("tool_call") == 0 {
		t.Fatalf("panics not reported: %+v", sink.events)
	}
}

func TestInProc_IPC05_InitErrorSkipped(t *testing.T) {
	sink := &eventSink{}
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: sink.emit, Clock: testkit.NewFakeClock(time.Unix(0, 0))})
	_ = h.AddBundled(&funcBundled{name: "broken", init: func(env *Env) error {
		return context.DeadlineExceeded
	}})
	errs := h.Start(context.Background())
	if len(errs) != 1 {
		t.Fatalf("Start errors = %v, want one", errs)
	}
	if sink.countType(proto.EventExtensionError) == 0 {
		t.Fatal("no extension_error emitted")
	}
}

func TestInProc_IPC06_InitOncePerStart(t *testing.T) {
	var mu sync.Mutex
	inits := 0
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: (&eventSink{}).emit, Clock: testkit.NewFakeClock(time.Unix(0, 0))})
	_ = h.AddBundled(&funcBundled{name: "counted", init: func(env *Env) error {
		mu.Lock()
		inits++
		mu.Unlock()
		return nil
	}})
	_ = h.Start(context.Background())
	_ = h.Start(context.Background()) // a second Start must not re-init
	if got := inits; got != 1 {
		t.Fatalf("inits after two Starts = %d, want 1", got)
	}
	_ = h.Reload(context.Background())
	if got := inits; got != 2 {
		t.Fatalf("inits after reload = %d, want 2", got)
	}
}

func TestInProc_IPC07_EventsOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: (&eventSink{}).emit})
	_ = h.AddBundled(&funcBundled{name: "obs", init: func(env *Env) error {
		env.Events(func(ev proto.Event) {
			mu.Lock()
			order = append(order, "a:"+ev.Type)
			mu.Unlock()
		})
		env.Events(func(ev proto.Event) {
			mu.Lock()
			order = append(order, "b:"+ev.Type)
			mu.Unlock()
		})
		return nil
	}})
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	h.Observe(proto.Event{Type: "one"})
	h.Observe(proto.Event{Type: "two"})
	mu.Lock()
	defer mu.Unlock()
	want := []string{"a:one", "b:one", "a:two", "b:two"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestInProc_IPC08_SlowEventCallback(t *testing.T) {
	clock := testkit.NewFakeClock(time.Unix(0, 0))
	sink := &eventSink{}
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	var mu sync.Mutex
	fast := 0
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: sink.emit, Clock: clock})
	_ = h.AddBundled(&funcBundled{name: "slow", init: func(env *Env) error {
		env.Events(func(proto.Event) {
			started <- struct{}{}
			<-release
		})
		env.Events(func(proto.Event) {
			mu.Lock()
			fast++
			mu.Unlock()
		})
		return nil
	}})
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}

	observe := func(typ string) {
		done := make(chan struct{})
		go func() {
			h.Observe(proto.Event{Type: typ})
			close(done)
		}()
		// The slow observer signals once it is running; its budget timer was
		// registered before the goroutine started, so advancing now is safe.
		<-started
		clock.Advance(eventCallbackBudget)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Observe did not return past the budget")
		}
	}

	observe("e1")
	if got := sink.countHook("events"); got != 1 {
		t.Fatalf("reports after e1 = %d, want 1", got)
	}
	observe("e2") // within the minute: not reported again
	if got := sink.countHook("events"); got != 1 {
		t.Fatalf("reports after e2 = %d, want 1", got)
	}
	clock.Advance(time.Minute)
	observe("e3")
	if got := sink.countHook("events"); got != 2 {
		t.Fatalf("reports after e3 = %d, want 2", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if fast != 3 {
		t.Fatalf("fast observer runs = %d, want 3", fast)
	}
}

func TestInProc_IPC09_HasUI(t *testing.T) {
	var withUI, withoutUI bool
	caps := map[string]bool{"ui.form": true}
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: (&eventSink{}).emit, HasUI: func(c string) bool { return caps[c] }})
	_ = h.AddBundled(&funcBundled{name: "ui", init: func(env *Env) error {
		withUI = env.HasUI("form") && env.HasUI("ui.form")
		return nil
	}})
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	h2 := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: (&eventSink{}).emit})
	_ = h2.AddBundled(&funcBundled{name: "noui", init: func(env *Env) error {
		withoutUI = env.HasUI("form")
		return nil
	}})
	_ = h2.Start(context.Background())
	if !withUI {
		t.Fatal("HasUI(form) = false with a client")
	}
	if withoutUI {
		t.Fatal("HasUI(form) = true without a client")
	}
}

func TestInProc_IPC10_DisableRemoves(t *testing.T) {
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Disabled: []string{"tasks"}, Emit: (&eventSink{}).emit, Clock: testkit.NewFakeClock(time.Unix(0, 0))})
	_ = h.AddBundled(&funcBundled{name: "tasks", init: func(env *Env) error {
		if err := env.Tool(textTool("todo", "x", tool.Info{})); err != nil {
			return err
		}
		return env.Command(InProcCommand{Name: "todos", Run: func(context.Context, string) (string, error) { return "", nil }})
	}})
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	if _, ok := h.Registry().Get("todo"); ok {
		t.Fatal("disabled extension registered a tool")
	}
	if len(h.Commands()) != 0 {
		t.Fatalf("disabled extension registered commands: %+v", h.Commands())
	}
}

func TestInProc_IPC11_HooksComposeWithSubprocess(t *testing.T) {
	t.Setenv("ECHO_HOOKS", "tool_call")
	t.Setenv("ECHO_HOOK_ARGS", `{"n":2}`)
	dir := echoDir(t, "thirdparty", "")
	var mu sync.Mutex
	var seen string
	h := NewHost(HostOptions{Dirs: []string{dir}, Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: (&eventSink{}).emit, Clock: testkit.NewFakeClock(time.Unix(0, 0))})
	_ = h.AddBundled(&funcBundled{name: "hooks", init: func(env *Env) error {
		env.Hooks(agent.Hooks{ToolCall: func(_ context.Context, c agent.ToolCall) *agent.CallDecision {
			mu.Lock()
			seen = string(c.Arguments)
			mu.Unlock()
			return &agent.CallDecision{Arguments: json.RawMessage(`{"n":1}`)}
		}})
		return nil
	}})
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x", Arguments: json.RawMessage(`{"n":0}`)})
	mu.Lock()
	got := seen
	mu.Unlock()
	if got != `{"n":0}` {
		t.Fatalf("bundled hook saw %q, want the original (bundled runs first)", got)
	}
	if dec == nil || string(dec.Arguments) != `{"n":2}` {
		t.Fatalf("final decision = %+v, want the subprocess rewrite", dec)
	}
}

func TestInProc_IPC12_NotuiLogic(t *testing.T) {
	h := NewHost(HostOptions{Registry: tool.NewRegistry(), Session: &fakeSession{}, Emit: (&eventSink{}).emit, Clock: testkit.NewFakeClock(time.Unix(0, 0))})
	_ = h.AddBundled(&funcBundled{name: "plain", init: func(env *Env) error {
		return env.Tool(textTool("plain_tool", "works", tool.Info{ReadOnly: true}))
	}})
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("Start: %v", errs)
	}
	got := h.Registry().Execute(context.Background(), "plain_tool", nil, nil)
	if got.Content != "works" {
		t.Fatalf("result = %q", got.Content)
	}
}
