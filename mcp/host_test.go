package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/tool"
)

// warnLog collects the host's warnings from whichever goroutine reports them.
type warnLog struct {
	mu   sync.Mutex
	errs []error
}

func (w *warnLog) add(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.errs = append(w.errs, err)
}

func (w *warnLog) all() []error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]error(nil), w.errs...)
}

// hostFor builds a host over servers named "srv" in one role, plus extra args.
func hostFor(t *testing.T, role string, extra ...string) (*Host, *tool.Registry, *warnLog) {
	t.Helper()
	return hostWith(t, config.MCPConfig{Servers: []config.MCPServer{{
		Name: "srv", Command: serverBin,
		Args:      append([]string{"-role", role}, extra...),
		TimeoutMS: 5000,
	}}})
}

// hostWith builds a host over the given config and collects its warnings.
func hostWith(t *testing.T, cfg config.MCPConfig) (*Host, *tool.Registry, *warnLog) {
	t.Helper()
	reg := tool.NewRegistry()
	warns := &warnLog{}
	h := NewHost(cfg, HostOptions{
		Registry: reg,
		Inherit:  []string{"PATH=" + os.Getenv("PATH")},
		Version:  "test",
		Warn:     warns.add,
	})
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })
	return h, reg, warns
}

func TestHost_MCP02_RegistersEveryTool(t *testing.T) {
	h, reg, warned := hostFor(t, "ok")
	errs := h.Start(context.Background())
	if len(errs)+len(warned.all()) != 0 {
		t.Fatalf("warnings = %v / %v, want none", errs, warned.all())
	}
	echo := toolName(t, reg, "srv__echo")
	if echo.Name != "srv__echo" {
		t.Fatalf("name = %q, want the server prefix", echo.Name)
	}
	if !strings.HasPrefix(echo.Description, "[srv] ") {
		t.Fatalf("description = %q, want the provenance prefix", echo.Description)
	}
	if echo.Parameters["type"] != "object" {
		t.Fatalf("schema = %v, want the server's input schema", echo.Parameters)
	}
	if !echo.ReadOnly || echo.OpenWorld {
		t.Fatalf("annotations = %+v, want read-only and not open-world", echo)
	}
	if info, _ := reg.Info("srv__fail"); !info.Destructive {
		t.Fatalf("fail tool = %+v, want destructive", info)
	}
	if got, want := len(reg.Names()), 4; got != want {
		t.Fatalf("registered = %v, want four tools", reg.Names())
	}
}

func TestHost_MCP06_ServerThatExitsIsAWarning(t *testing.T) {
	h, reg, warned := hostFor(t, "exit")
	errs := h.Start(context.Background())
	// One warning however it arrived: a server that dies during the handshake is
	// noticed by the reader and by the dial, and only the first reporter speaks.
	waitWarnings(t, errs, warned, 1)
	if names := reg.Names(); len(names) != 0 {
		t.Fatalf("registered = %v, want nothing from a dead server", names)
	}
	status := h.Status()
	if len(status) != 1 || status[0].Name != "srv" {
		t.Fatalf("status = %+v", status)
	}
	if status[0].State != StateFailed || status[0].Error == "" {
		t.Fatalf("status = %+v, want failed with a reason", status[0])
	}
	// The run continues: a second Start on the same host is harmless.
	h.Start(context.Background())
}

func TestHost_MCP09_AnOccupiedNameIsAWarning(t *testing.T) {
	h, reg, warned := hostFor(t, "ok")
	taken := tool.New(tool.Info{Name: "srv__echo", Description: "already here"},
		func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			return tool.Text("taken"), nil
		})
	if err := reg.Register(taken); err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("connect warnings = %v, want none", errs)
	}
	var found bool
	for _, err := range warned.all() {
		if strings.Contains(err.Error(), `"srv__echo"`) && strings.Contains(err.Error(), "echo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want one naming the taken tool", warned.all())
	}
	// The first registration keeps the name, and the other tools still land.
	if got, _ := reg.Get("srv__echo"); got == nil {
		t.Fatal("the taken name was lost")
	}
	if res := reg.Execute(context.Background(), "srv__echo", nil, &tool.Context{}); res.Content != "taken" {
		t.Fatalf("call = %q, want the first registration to keep the name", res.Content)
	}
	if _, ok := reg.Get("srv__image"); !ok {
		t.Fatalf("registered = %v, want the other tools registered", reg.Names())
	}
}

func TestHost_MCP10_ToolListChangedRefreshes(t *testing.T) {
	h, reg, warned := hostFor(t, "listchanged")
	if errs := h.Start(context.Background()); len(errs)+len(warned.all()) != 0 {
		t.Fatalf("warnings = %v / %v, want none", errs, warned.all())
	}
	if _, ok := reg.Get("srv__late"); ok {
		t.Fatal("the late tool was registered before the notification")
	}
	// The notification arrives just after the first tools/list, so poll for the
	// registration it triggers: a bounded poll, because the other end of the
	// socket is the only thing that can tell us it landed.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok := reg.Get("srv__late"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registered = %v, want srv__late after tools/list_changed", reg.Names())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitWarnings waits for want warnings across both channels and fails if a
// second one ever arrives. The report runs on the reader's goroutine, so the
// only thing that can say it landed is the wait itself.
func waitWarnings(t *testing.T, errs []error, warned *warnLog, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := len(errs) + len(warned.all())
		if got > want {
			t.Fatalf("warnings = %d (%v / %v), want %d and never two", got, errs, warned.all(), want)
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("warnings = %d (%v / %v), want %d", got, errs, warned.all(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// alive reports whether a process still exists, and waitGone polls until it does
// not: the operating system is the only evidence a child exited.
func waitGone(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still alive after %v", pid, within)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// toolName returns the tool's Info, failing the test when it is missing.
func toolName(t *testing.T, reg *tool.Registry, name string) tool.Info {
	t.Helper()
	info, ok := reg.Info(name)
	if !ok {
		t.Fatalf("registered = %v, want %s", reg.Names(), name)
	}
	return info
}

func TestHost_StartingIsNotFailed(t *testing.T) {
	// A noinit server never answers initialize, so the dial is still in flight
	// while the host is asked for its status: that reads as starting, never as
	// failed, and a /mcp or doctor line must not call it dead.
	h, _, _ := hostWith(t, config.MCPConfig{Servers: []config.MCPServer{{
		Name: "srv", Command: serverBin, Args: []string{"-role", "noinit"}, TimeoutMS: 1000,
	}}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Start(context.Background())
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status := h.Status()
		if len(status) == 1 && status[0].State == StateStarting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %+v, want starting while the dial is in flight", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	<-done
	if status := h.Status(); status[0].State != StateFailed {
		t.Fatalf("status = %+v, want failed once the dial gave up", status)
	}
}

func TestHost_MCP15_AServerThatDiesIsReported(t *testing.T) {
	// The server answers the handshake and its tool list, then exits: the
	// connection is good, and then it is not.
	h, reg, warned := hostFor(t, "crash-after-list")
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("connect warnings = %v, want none: the handshake succeeded", errs)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok := reg.Get("srv__echo"); !ok && len(h.Status()) == 1 && h.Status()[0].State == StateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %+v, registered = %v: want the death noticed", h.Status(), reg.Names())
		}
		time.Sleep(10 * time.Millisecond)
	}
	status := h.Status()[0]
	if status.Tools != 0 {
		t.Fatalf("status = %+v, want no tools reported", status)
	}
	if status.Error == "" || !strings.Contains(status.Error, "connection lost") {
		t.Fatalf("status = %+v, want the reason", status)
	}
	if len(warned.all()) != 1 {
		t.Fatalf("warnings = %v, want exactly one for the death", warned.all())
	}
}

func TestHost_MCP16_ServersDoNotInheritCredentials(t *testing.T) {
	reg := tool.NewRegistry()
	secret := "sk-should-not-be-here"
	env := []string{
		"PATH=" + os.Getenv("PATH"), "OPENROUTER_API_KEY=" + secret, "MINE=" + secret,
		"GITHUB_TOKEN=" + secret, "OTHER_API_KEY=" + secret,
	}
	h := NewHost(config.MCPConfig{Servers: []config.MCPServer{{
		Name: "srv", Command: serverBin, Args: []string{"-role", "ok"}, TimeoutMS: 5000,
		// The config's own env: is added to the inherited environment.
		Env: map[string]string{"SERVER_OWN": "yes"},
	}}}, HostOptions{
		Registry: reg,
		Inherit:  env,
		// A broad pattern hides both keys; the pass list keeps the one the user
		// asked for, exactly as ext treats extensions.pass_env.
		Scrub:   []string{"OPENROUTER_API_KEY", "_API_KEY"},
		Pass:    []string{"GITHUB_TOKEN"},
		Version: "test",
	})
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("start = %v", errs)
	}
	answer := func(name string) string {
		res := reg.Execute(context.Background(), "srv__env", json.RawMessage(`{"name":"`+name+`"}`), &tool.Context{})
		return res.Content
	}
	if got := answer("OPENROUTER_API_KEY"); got != "" {
		t.Fatalf("server saw OPENROUTER_API_KEY = %q, want it scrubbed", got)
	}
	// The pass list keeps a name a broad pattern would have hidden, which is what
	// extensions.pass_env means for an extension.
	if got := answer("GITHUB_TOKEN"); got != secret {
		t.Fatalf("server saw GITHUB_TOKEN = %q, want the passed name kept", got)
	}
	if got := answer("MINE"); got != secret {
		t.Fatalf("server saw MINE = %q, want the rest of the environment", got)
	}
	if got := answer("PATH"); got == "" {
		t.Fatal("server saw no PATH, want the inherited environment")
	}
	if got := answer("SERVER_OWN"); got != "yes" {
		t.Fatalf("server saw SERVER_OWN = %q, want the config's env added", got)
	}
	if got := answer("OTHER_API_KEY"); got != "" {
		t.Fatalf("server saw OTHER_API_KEY = %q, want the pattern to hide it", got)
	}
}

func TestHost_MCP15_DeathDuringTheFirstListingIsOneWarning(t *testing.T) {
	// The handshake succeeds and the process dies before tools/list is answered:
	// the death and the failed listing are the same event, and saying so twice
	// would teach a user to ignore both.
	h, reg, warned := hostFor(t, "crash-after-init")
	errs := h.Start(context.Background())
	waitWarnings(t, errs, warned, 1)
	if names := reg.Names(); len(names) != 0 {
		t.Fatalf("registered = %v, want nothing from a server that died", names)
	}
}

func TestHost_MCP08_DisconnectStopsTheProcess(t *testing.T) {
	// A malformed frame disconnects the server; the process is still alive at
	// that moment, so the disconnect has to end it or it outlives the host.
	h, reg, _ := hostFor(t, "malformed")
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("start = %v", errs)
	}
	pid := h.servers[0].client.Pid()
	res := reg.Execute(context.Background(), "srv__echo", nil, &tool.Context{})
	if !res.IsError {
		t.Fatalf("call = %+v, want a failed result from a broken server", res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if h.Status()[0].State == StateFailed && !alive(pid) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %+v, pid %d alive = %v: want the process gone", h.Status()[0], pid, alive(pid))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
