package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/tool"
)

// hostFor builds a host over servers named "srv" in one role, plus extra args.
func hostFor(t *testing.T, role string, extra ...string) (*Host, *tool.Registry, *[]error) {
	t.Helper()
	return hostWith(t, config.MCPConfig{Servers: []config.MCPServer{{
		Name: "srv", Command: serverBin,
		Args:      append([]string{"-role", role}, extra...),
		TimeoutMS: 5000,
	}}})
}

// hostWith builds a host over the given config and collects its warnings.
func hostWith(t *testing.T, cfg config.MCPConfig) (*Host, *tool.Registry, *[]error) {
	t.Helper()
	reg := tool.NewRegistry()
	var warns []error
	h := NewHost(cfg, HostOptions{
		Registry: reg,
		Inherit:  []string{"PATH=" + os.Getenv("PATH")},
		Version:  "test",
		Warn:     func(err error) { warns = append(warns, err) },
	})
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })
	return h, reg, &warns
}

func TestHost_MCP02_RegistersEveryTool(t *testing.T) {
	h, reg, warns := hostFor(t, "ok")
	h.Start(context.Background())
	if len(*warns) != 0 {
		t.Fatalf("warnings = %v, want none", *warns)
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
	if got, want := len(reg.Names()), 3; got != want {
		t.Fatalf("registered = %v, want three tools", reg.Names())
	}
}

func TestHost_MCP06_ServerThatExitsIsAWarning(t *testing.T) {
	h, reg, warns := hostFor(t, "exit")
	h.Start(context.Background())
	if len(*warns) != 1 {
		t.Fatalf("warnings = %v, want one for the dead server", *warns)
	}
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
	h, reg, warns := hostFor(t, "ok")
	taken := tool.New(tool.Info{Name: "srv__echo", Description: "already here"},
		func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			return tool.Text("taken"), nil
		})
	if err := reg.Register(taken); err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	h.Start(context.Background())
	var found bool
	for _, err := range *warns {
		if strings.Contains(err.Error(), `"srv__echo"`) && strings.Contains(err.Error(), "echo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want one naming the taken tool", *warns)
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
	h, reg, warns := hostFor(t, "listchanged")
	h.Start(context.Background())
	if len(*warns) != 0 {
		t.Fatalf("warnings = %v, want none", *warns)
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
