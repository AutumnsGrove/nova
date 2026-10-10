package ext

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// waitContains polls path until it contains substr or the deadline passes.
func waitContains(t *testing.T, path, substr string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(path)
		last = string(b)
		if strings.Contains(last, substr) {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("trace %s never contained %q; got:\n%s", path, substr, last)
	return last
}

func TestHost_HST02_ToolTimeout(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{
		Registry: reg, Clock: clk,
		Env: envWith("ECHO_TOOL_TIMEOUT_MS=100", "ECHO_SLOW_TOOL_MS=400"),
	})
	_ = h
	done := make(chan tool.Result, 1)
	go func() {
		done <- reg.Execute(context.Background(), "echo", json.RawMessage(`{}`), &tool.Context{})
	}()
	clk.BlockUntil(t, 2)
	clk.Advance(150 * time.Millisecond)
	res := <-done
	if !res.IsError || !strings.Contains(res.Content, "did not answer in 100ms") {
		t.Fatalf("result = %+v", res)
	}
}

func TestHost_HST03_CrashDuringCall(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{Registry: reg, Env: envWith("ECHO_EXIT_AFTER=1")})
	res := reg.Execute(context.Background(), "echo", json.RawMessage(`{}`), &tool.Context{})
	if !res.IsError || !strings.Contains(res.Content, "exited") {
		t.Fatalf("result = %+v", res)
	}
}

func TestHost_HST04_CancelDuringCall(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{Registry: reg, Env: envWith("ECHO_SLOW_TOOL_MS=3000")})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan tool.Result, 1)
	go func() {
		done <- reg.Execute(ctx, "echo", json.RawMessage(`{}`), &tool.Context{})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		if !res.IsError || !strings.Contains(res.Content, "cancelled") {
			t.Fatalf("result = %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not return promptly")
	}
}

func TestHost_HST05_BuiltinConflict(t *testing.T) {
	reg := tool.NewRegistry()
	_ = reg.Register(tool.New(tool.Info{Name: "read"}, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		return tool.Text("x"), nil
	}))
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{Registry: reg, Env: envWith("ECHO_TOOL_NAMES=read,other")})
	if _, ok := reg.Get("other"); !ok {
		t.Fatalf("other tool not registered: %v", reg.Names())
	}
}

func TestHost_HST06_ExtensionConflict(t *testing.T) {
	reg := tool.NewRegistry()
	dir1 := echoDir(t, "one", "")
	dir2 := echoDir(t, "two", "")
	startHost(t, []string{dir1, dir2}, HostOptions{Registry: reg})
	count := 0
	for _, n := range reg.Names() {
		if n == "echo" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("echo registered %d times: %v", count, reg.Names())
	}
}

func TestHost_HST07_ReregisterReplaces(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Registry: reg, Env: envWith("ECHO_REGISTER_TWICE=1")})
	if names := reg.Names(); len(names) != 1 || names[0] != "echo" {
		t.Fatalf("names = %v", names)
	}
	if h.Status()[0].State != stateRunning {
		t.Fatalf("status = %+v", h.Status())
	}
}

func TestHost_HST08_CapabilityNotDeclared(t *testing.T) {
	ui := &fakeUI{}
	dir := rawEchoDir(t, "echo", "capabilities: [tools]\n")
	startHost(t, []string{dir}, HostOptions{UI: ui, Env: envWith("ECHO_UI=confirm")})
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if len(ui.requests) != 0 {
		t.Fatalf("ui_request was forwarded without capability: %+v", ui.requests)
	}
}

func TestHost_HST09_CapabilityDeclared(t *testing.T) {
	ui := &fakeUI{}
	dir := rawEchoDir(t, "echo", "capabilities: [ui:confirm]\n")
	startHost(t, []string{dir}, HostOptions{UI: ui, Env: envWith("ECHO_UI=confirm")})
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if len(ui.requests) != 1 {
		t.Fatalf("requests = %+v", ui.requests)
	}
}

func TestHost_HST10_UIAnswerReachesExtension(t *testing.T) {
	ui := &fakeUI{answer: proto.UIResponse{OK: true}}
	trace := t.TempDir() + "/trace"
	dir := rawEchoDir(t, "echo", "capabilities: [ui:confirm]\n")
	startHost(t, []string{dir}, HostOptions{UI: ui, Env: envWith("ECHO_UI=confirm", "ECHO_TRACE="+trace)})
	waitContains(t, trace, "ui_response", 3*time.Second)
}

func TestHost_HST11_UnsupportedUI(t *testing.T) {
	ui := &fakeUI{err: context.DeadlineExceeded}
	trace := t.TempDir() + "/trace"
	dir := rawEchoDir(t, "echo", "capabilities: [ui:confirm]\n")
	startHost(t, []string{dir}, HostOptions{UI: ui, Env: envWith("ECHO_UI=confirm", "ECHO_TRACE="+trace)})
	waitContains(t, trace, "error", 3*time.Second)
}

func TestHost_HST12_FireAndForgetUI(t *testing.T) {
	ui := &fakeUI{}
	dir := rawEchoDir(t, "echo", "capabilities: [ui:notify]\n")
	startHost(t, []string{dir}, HostOptions{UI: ui, Env: envWith("ECHO_UI=notify")})
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if len(ui.notices) != 1 || ui.notices[0].Method != "notify" {
		t.Fatalf("notices = %+v", ui.notices)
	}
}

func TestHost_HST13_AppendEntry(t *testing.T) {
	sess := &fakeSession{}
	dir := rawEchoDir(t, "echo", "capabilities: [session:append_entry]\n")
	h := startHost(t, []string{dir}, HostOptions{Session: sess, Env: envWith("ECHO_APPEND=todo.state")})
	_ = h
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.entries) != 1 || sess.entries[0].kind != "todo.state" || !sess.entries[0].keep {
		t.Fatalf("entries = %+v", sess.entries)
	}
}

func TestHost_HST17_Complete(t *testing.T) {
	trace := t.TempDir() + "/trace"
	calls := 0
	dir := rawEchoDir(t, "echo", "capabilities: [model:complete]\n")
	startHost(t, []string{dir}, HostOptions{
		Env: envWith("ECHO_COMPLETE_COUNT=1", "ECHO_TRACE="+trace),
		Complete: func(context.Context, provider.Request) (*provider.Response, error) {
			calls++
			return &provider.Response{}, nil
		},
	})
	waitContains(t, trace, "complete_result", 3*time.Second)
	if calls != 1 {
		t.Fatalf("Complete called %d times", calls)
	}
}

func TestHost_HST18_CompleteCap(t *testing.T) {
	calls := 0
	dir := rawEchoDir(t, "echo", "capabilities: [model:complete]\n")
	startHost(t, []string{dir}, HostOptions{
		CompleteMax: 1,
		Env:         envWith("ECHO_COMPLETE_COUNT=2"),
		Complete: func(context.Context, provider.Request) (*provider.Response, error) {
			calls++
			return &provider.Response{}, nil
		},
	})
	time.Sleep(300 * time.Millisecond)
	if calls != 1 {
		t.Fatalf("Complete called %d times, want 1", calls)
	}
}

func TestHost_HST19_Commands(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Registry: reg})
	found := false
	for _, c := range h.Commands() {
		if c.Name == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("commands = %+v", h.Commands())
	}
	out, err := h.RunCommand(context.Background(), "hello", "")
	if err != nil || out != "command ok" {
		t.Fatalf("RunCommand = %q, %v", out, err)
	}
}

func TestHost_HST20_CommandToStoppedExtension(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Registry: reg})
	h.mu.Lock()
	inst := h.insts[0]
	h.mu.Unlock()
	inst.stop(context.Background())
	if _, err := h.RunCommand(context.Background(), "hello", ""); err == nil {
		t.Fatal("expected an error for a stopped extension")
	}
}

func TestHost_HST21_DuplicateAnswer(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{Registry: reg, Env: envWith("ECHO_ANSWER_TWICE=1")})
	res := reg.Execute(context.Background(), "echo", json.RawMessage(`{}`), &tool.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
}

func TestHost_HST22_ConcurrentCalls(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{Registry: reg})
	var wg sync.WaitGroup
	errs := make(chan tool.Result, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- reg.Execute(context.Background(), "echo", json.RawMessage(`{}`), &tool.Context{})
		}()
	}
	wg.Wait()
	close(errs)
	for res := range errs {
		if res.IsError {
			t.Fatalf("result = %+v", res)
		}
	}
}

func TestHost_HST23_FlagsInInit(t *testing.T) {
	logs := &logCapture{}
	dir := echoDir(t, "echo", "flags:\n  - name: color\n    description: c\n    default: blue\n")
	startHost(t, []string{dir}, HostOptions{Log: logs.log, Env: envWith("ECHO_REPORT_FLAGS=1")})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.all(), "color") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("flags not seen in init: %s", logs.all())
}

func TestHost_HST24_Status(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Registry: reg})
	st := h.Status()
	if len(st) != 1 || st[0].Name != "echo" || st[0].Version != "1.0.0" || st[0].State != stateRunning {
		t.Fatalf("status = %+v", st)
	}
}
