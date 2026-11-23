//go:build unix

package mcp

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// alive reports whether a process still exists.
func alive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

// waitGone polls until the process is gone, because the only evidence that a
// child exited is the operating system.
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

func TestClient_MCP11_CloseEscalatesPastIgnoredSigterm(t *testing.T) {
	c := dial(t, "ignore-term")
	pid := c.Pid()
	start := time.Now()
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	waitGone(t, pid, 10*time.Second)
	if elapsed := time.Since(start); elapsed > 9*time.Second {
		t.Fatalf("close took %v, want the escalation to finish inside the grace windows", elapsed)
	}
}

func TestHost_MCP11_ReloadReplacesTheProcess(t *testing.T) {
	h, reg, warned := hostFor(t, "ok")
	if errs := h.Start(context.Background()); len(errs)+len(warned.all()) != 0 {
		t.Fatalf("warnings = %v / %v", errs, warned.all())
	}
	first := h.servers[0].client.Pid()
	if !alive(first) {
		t.Fatalf("server pid %d is not running", first)
	}
	if warns := h.Reload(context.Background()); len(warns) != 0 {
		t.Fatalf("reload warnings = %v", warns)
	}
	second := h.servers[0].client.Pid()
	if second == first {
		t.Fatalf("reload kept pid %d, want a new process", second)
	}
	waitGone(t, first, 10*time.Second)
	if _, ok := reg.Get("srv__echo"); !ok {
		t.Fatalf("registered = %v, want the tools re-registered after a reload", reg.Names())
	}

	if err := h.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	waitGone(t, second, 10*time.Second)
	if names := reg.Names(); len(names) != 0 {
		t.Fatalf("registered = %v, want nothing after shutdown", names)
	}
}
