package ext

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// rawHost builds a host and cleans it up, advancing a fake clock so shutdown
// grace timers always fire.
func rawHost(t *testing.T, opt HostOptions) *Host {
	t.Helper()
	h := NewHost(opt)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stop := make(chan struct{})
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
					if clk, ok := opt.Clock.(*testkit.FakeClock); ok {
						clk.Advance(time.Second)
					}
					time.Sleep(time.Millisecond)
				}
			}
		}()
		_ = h.Shutdown(ctx)
		close(stop)
	})
	return h
}

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition never held: %s", msg)
}

// advanceUntil advances a fake clock in small steps until cond holds.
func advanceUntil(t *testing.T, clk *testkit.FakeClock, cond func() bool, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		clk.Advance(250 * time.Millisecond)
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition never held after advancing the clock")
	}
}

// stateOf returns one instance's state.
func stateOf(h *Host, name string) string {
	for _, s := range h.Status() {
		if s.Name == name {
			return s.State
		}
	}
	return ""
}

func TestSupervise_SUP02_NeverRegisters(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	reg := tool.NewRegistry()
	bad := echoDir(t, "bad", "")
	good := echoDir(t, "good", "")
	h := rawHost(t, HostOptions{Dirs: []string{bad, good}, Registry: reg, Clock: clk, Env: envWith("ECHO_NO_REGISTER=bad")})
	done := make(chan []error, 1)
	go func() { done <- h.Start(context.Background()) }()
	clk.BlockUntil(t, 1)
	clk.Advance(11 * time.Second)
	var errs []error
	select {
	case errs = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start hung")
	}
	if len(errs) == 0 {
		t.Fatal("expected an error for the extension that never registered")
	}
	waitFor(t, func() bool { return stateOf(h, "good") == stateRunning }, "good extension running")
	if got := stateOf(h, "bad"); got != stateFailed {
		t.Fatalf("bad state = %q, want failed", got)
	}
}

func TestSupervise_SUP03_CrashOnStart(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	reg := tool.NewRegistry()
	bad := echoDir(t, "bad", "")
	good := echoDir(t, "good", "")
	h := rawHost(t, HostOptions{Dirs: []string{bad, good}, Registry: reg, Clock: clk, Env: envWith("ECHO_CRASH_ON_START=bad")})
	errs := h.Start(context.Background())
	if len(errs) == 0 {
		t.Fatal("expected an error for the crashing extension")
	}
	waitFor(t, func() bool { return stateOf(h, "good") == stateRunning }, "good extension running")
	if got := stateOf(h, "bad"); got != stateFailed {
		t.Fatalf("bad state = %q, want failed", got)
	}
	if _, ok := reg.Get("echo"); !ok {
		t.Fatal("good tool not registered")
	}
}

func TestSupervise_SUP04_InvalidOutputBeforeRegister(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	dir := rawEchoDir(t, "echo", "")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Clock: clk, Env: envWith("ECHO_GARBAGE=1", "ECHO_NO_REGISTER=1")})
	done := make(chan []error, 1)
	go func() { done <- h.Start(context.Background()) }()
	clk.BlockUntil(t, 1)
	clk.Advance(11 * time.Second)
	select {
	case errs := <-done:
		if len(errs) == 0 {
			t.Fatal("expected failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start hung")
	}
}

func TestSupervise_SUP05_StderrForwarded(t *testing.T) {
	logs := &logCapture{}
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{Log: logs.log, Env: envWith("ECHO_STDERR=1")})
	waitFor(t, func() bool { return strings.Contains(logs.all(), "this is stderr") }, "stderr line logged")
}

func TestSupervise_SUP06_EnvironmentScrubbed(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "topsecret")
	t.Setenv("FOO_API_KEY", "alsosecret")
	t.Setenv("X_TOKEN", "tokens")
	logs := &logCapture{}
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{Log: logs.log, Env: envWith("ECHO_REPORT_ENV=OPENROUTER_API_KEY,FOO_API_KEY,X_TOKEN")})
	waitFor(t, func() bool { return strings.Count(logs.all(), "ENV ") >= 3 }, "env report")
	if strings.Contains(logs.all(), "topsecret") || strings.Contains(logs.all(), "alsosecret") || strings.Contains(logs.all(), "tokens") {
		t.Fatalf("a secret leaked to the child:\n%s", logs.all())
	}
}

func TestSupervise_SUP07_PassEnv(t *testing.T) {
	t.Setenv("FOO_API_KEY", "visible")
	logs := &logCapture{}
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{
		Log: logs.log, PassEnv: []string{"FOO_API_KEY"},
		Env: envWith("ECHO_REPORT_ENV=FOO_API_KEY"),
	})
	waitFor(t, func() bool { return strings.Contains(logs.all(), "FOO_API_KEY=visible") }, "passed env")
}

func TestSupervise_SUP08_NovaVars(t *testing.T) {
	logs := &logCapture{}
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{
		Log: logs.log, NovaVersion: "9.9.9", CWD: "/tmp", Mode: "rpc",
		Env: envWith("ECHO_REPORT_ENV=NOVA_EXTENSION,NOVA_VERSION,NOVA_CWD,NOVA_MODE"),
	})
	waitFor(t, func() bool { return strings.Count(logs.all(), "NOVA_") >= 4 }, "nova vars")
	if !strings.Contains(logs.all(), "NOVA_VERSION=9.9.9") || !strings.Contains(logs.all(), "NOVA_MODE=rpc") {
		t.Fatalf("NOVA vars wrong:\n%s", logs.all())
	}
}

func TestSupervise_SUP09_CrashRestarted(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Registry: reg, Clock: clk, Env: envWith("ECHO_EXIT_AFTER=1")})
	h.Start(context.Background())
	_ = reg.Execute(context.Background(), "echo", nil, &tool.Context{})
	waitFor(t, func() bool { _, ok := reg.Get("echo"); return !ok }, "crash removed the tool")
	advanceUntil(t, clk, func() bool {
		for _, s := range h.Status() {
			if s.Name == "echo" && s.Restarts >= 1 && s.State == stateRunning {
				return true
			}
		}
		return false
	}, 5*time.Second)
	if _, ok := reg.Get("echo"); !ok {
		t.Fatal("tool not re-registered after restart")
	}
}

func TestSupervise_SUP10_RestartLimit(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	reg := tool.NewRegistry()
	var events int
	var mu sync.Mutex
	dir := echoDir(t, "echo", "restart: {max: 1}\n")
	h := rawHost(t, HostOptions{
		Dirs: []string{dir}, Registry: reg, Clock: clk,
		Env: envWith("ECHO_CRASH_AFTER_REGISTER=1"),
		Emit: func(ev proto.Event) {
			if ev.Type == proto.EventExtensionError {
				mu.Lock()
				events++
				mu.Unlock()
			}
		},
	})
	h.Start(context.Background())
	// Pump the fake clock while the real process crashes and restarts. A fixed
	// iteration count is racy under -race; wait for the disabled state instead.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && stateOf(h, "echo") != stateDisabled {
		clk.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
	waitFor(t, func() bool { return stateOf(h, "echo") == stateDisabled }, "disabled after restart limit")
	if _, ok := reg.Get("echo"); ok {
		t.Fatal("tool still registered after disable")
	}
	mu.Lock()
	defer mu.Unlock()
	if events < 1 {
		t.Fatalf("extension_error events = %d, want at least 1", events)
	}
}

func TestSupervise_SUP11_RestartCounterResets(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "restart: {max: 1}\n")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Registry: reg, Clock: clk, Env: envWith("ECHO_EXIT_AFTER=1")})
	h.Start(context.Background())
	_ = reg.Execute(context.Background(), "echo", nil, &tool.Context{})
	waitFor(t, func() bool { _, ok := reg.Get("echo"); return !ok }, "crash removed the tool")
	advanceUntil(t, clk, func() bool { return stateOf(h, "echo") == stateRunning }, 5*time.Second)
	clk.Advance(61 * time.Second)
	_ = reg.Execute(context.Background(), "echo", nil, &tool.Context{})
	waitFor(t, func() bool { _, ok := reg.Get("echo"); return !ok }, "second crash removed the tool")
	advanceUntil(t, clk, func() bool { return stateOf(h, "echo") == stateRunning }, 5*time.Second)
	if stateOf(h, "echo") == stateDisabled {
		t.Fatal("extension was disabled despite a healthy run")
	}
}

func TestSupervise_SUP12_BackoffDelays(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	reg := tool.NewRegistry()
	marker := t.TempDir() + "/marker"
	dir := echoDir(t, "echo", "")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Registry: reg, Clock: clk, Env: envWith("ECHO_CRASH_ONCE=1", "ECHO_CRASH_MARKER="+marker)})
	h.Start(context.Background())
	waitFor(t, func() bool { _, ok := reg.Get("echo"); return !ok }, "crash removed the tool")
	time.Sleep(50 * time.Millisecond) // let the backoff timer register
	clk.Advance(500 * time.Millisecond)
	if _, ok := reg.Get("echo"); ok {
		t.Fatal("restarted before the 1s backoff elapsed")
	}
	advanceUntil(t, clk, func() bool { _, ok := reg.Get("echo"); return ok }, 5*time.Second)
}

func TestSupervise_SUP13_CleanShutdown(t *testing.T) {
	dir := echoDir(t, "echo", "")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Clock: testkit.NewFakeClock(time.Unix(0, 0)), Env: os.Environ})
	h.Start(context.Background())
	if err := h.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, s := range h.Status() {
		if s.Restarts != 0 {
			t.Fatalf("clean shutdown counted as crash: %+v", s)
		}
	}
}

func TestSupervise_SUP14_KillsDescendants(t *testing.T) {
	trace := t.TempDir() + "/trace"
	dir := echoDir(t, "echo", "")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Clock: testkit.NewFakeClock(time.Unix(0, 0)), Env: envWith("ECHO_FORK=1", "ECHO_TRACE="+trace)})
	h.Start(context.Background())
	waitContains(t, trace, "CHILD", 3*time.Second)
	b, _ := os.ReadFile(trace)
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		t.Fatalf("no child pid in trace: %q", b)
	}
	pid, _ := strconv.Atoi(fields[1])
	if err := h.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return true
		}
		return proc.Signal(syscall.Signal(0)) != nil
	}, "descendant process killed")
}

func TestSupervise_SUP15_IgnoresShutdown(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	dir := echoDir(t, "echo", "")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Clock: clk, Env: envWith("ECHO_IGNORE_SHUTDOWN=1")})
	h.Start(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Shutdown(context.Background()) }()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-done:
			return
		case <-deadline:
			t.Fatal("Shutdown did not kill the extension that ignored it")
		default:
			clk.Advance(500 * time.Millisecond)
			time.Sleep(time.Millisecond)
		}
	}
}

func TestSupervise_SUP16_GarbageIgnored(t *testing.T) {
	logs := &logCapture{}
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{Registry: reg, Log: logs.log, Env: envWith("ECHO_GARBAGE=1")})
	if _, ok := reg.Get("echo"); !ok {
		t.Fatal("extension did not register after garbage")
	}
	waitFor(t, func() bool { return strings.Contains(logs.all(), "not JSON") }, "garbage warning")
}

func TestSupervise_SUP17_BigFrame(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	dir := echoDir(t, "echo", "")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Clock: clk, Env: envWith("ECHO_BIG_FRAME=1")})
	errs := h.Start(context.Background())
	if len(errs) == 0 {
		t.Fatal("expected failure for an over-long frame")
	}
}

func TestSupervise_SUP18_NoLeaks(t *testing.T) {
	testkit.NoLeaks(t)
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Registry: reg, Clock: testkit.NewFakeClock(time.Unix(0, 0)), Env: os.Environ})
	h.Start(context.Background())
	_ = reg.Execute(context.Background(), "echo", nil, &tool.Context{})
	if err := h.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
