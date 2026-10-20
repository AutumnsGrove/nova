package ext

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// commandHookDir writes a command-hook-only extension.
func commandHookDir(t *testing.T, hooksYAML, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hook.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: cmd\nversion: 1.0.0\ndescription: x\nhooks:\n" + hooksYAML
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHooks_HK01_FirstBlockWins(t *testing.T) {
	one := echoDir(t, "one", "")
	two := echoDir(t, "two", "")
	h := startHost(t, []string{one, two}, HostOptions{Env: envWith(
		"ECHO_HOOKS=tool_call", "ECHO_HOOK_BLOCK=1",
		"ECHO_HOOK_REASON_one=first", "ECHO_HOOK_REASON_two=second")})
	dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x"})
	if dec == nil || !dec.Block || dec.Reason != "first" {
		t.Fatalf("decision = %+v", dec)
	}
}

func TestHooks_HK02_RewriteChains(t *testing.T) {
	one := echoDir(t, "one", "")
	two := echoDir(t, "two", "")
	h := startHost(t, []string{one, two}, HostOptions{Env: envWith(
		"ECHO_HOOKS=tool_call", `ECHO_HOOK_ARGS_one={"a":1}`, "ECHO_HOOK_ECHO=two")})
	dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x"})
	if dec == nil || string(dec.Arguments) != `{"a":1}` {
		t.Fatalf("decision = %+v", dec)
	}
}

func TestHooks_HK03_BlockAfterRewrite(t *testing.T) {
	one := echoDir(t, "one", "")
	two := echoDir(t, "two", "")
	h := startHost(t, []string{one, two}, HostOptions{Env: envWith(
		"ECHO_HOOKS=tool_call", `ECHO_HOOK_ARGS_one={"a":1}`,
		"ECHO_HOOK_BLOCK=two", "ECHO_HOOK_REASON_two=stop")})
	dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x"})
	if dec == nil || !dec.Block || dec.Reason != "stop" {
		t.Fatalf("decision = %+v", dec)
	}
}

func TestHooks_HK04_ToolResultChain(t *testing.T) {
	one := echoDir(t, "one", "")
	two := echoDir(t, "two", "")
	h := startHost(t, []string{one, two}, HostOptions{Env: envWith(
		"ECHO_HOOKS=tool_result", "ECHO_HOOK_RESULT_one=A", "ECHO_HOOK_RESULT_two=B")})
	out := h.Hooks().ToolResult(context.Background(), agent.ToolCall{Name: "x"}, tool.Result{Content: "orig"})
	if out.Content != "B" {
		t.Fatalf("result = %+v", out)
	}
}

func TestHooks_HK05_TurnEndMerge(t *testing.T) {
	one := echoDir(t, "one", "")
	two := echoDir(t, "two", "")
	h := startHost(t, []string{one, two}, HostOptions{Env: envWith(
		"ECHO_HOOKS=turn_end", "ECHO_HOOK_END=one", "ECHO_HOOK_CONTINUE=two", "ECHO_HOOK_INJECT_two=hi")})
	dec := h.Hooks().TurnEnd(context.Background(), agent.Turn{Index: 0, Message: message.Assistant("x")})
	if !dec.End || !dec.Continue || len(dec.Inject) != 1 || dec.Inject[0].Content != "hi" {
		t.Fatalf("decision = %+v", dec)
	}
}

func TestHooks_HK06_HookDeadline(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	var mu sync.Mutex
	events := 0
	dir := echoDir(t, "echo", "")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Clock: clk, Env: envWith("ECHO_HOOKS=tool_call", "ECHO_HOOK_NO_ANSWER=1"),
		Emit: func(ev proto.Event) {
			if ev.Type == proto.EventExtensionError {
				mu.Lock()
				events++
				mu.Unlock()
			}
		}})
	h.Start(context.Background())
	call := func() {
		done := make(chan *agent.CallDecision, 1)
		go func() { done <- h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x"}) }()
		clk.BlockUntil(t, 2)
		clk.Advance(hookTimeout + time.Second)
		if dec := <-done; dec != nil {
			t.Fatalf("slow hook returned %+v", dec)
		}
	}
	call()
	call()
	mu.Lock()
	defer mu.Unlock()
	if events != 1 {
		t.Fatalf("extension_error events = %d, want 1 (rate limited)", events)
	}
}

func TestHooks_HK07_ExtensionDown(t *testing.T) {
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Env: envWith("ECHO_HOOKS=tool_call", "ECHO_HOOK_BLOCK=1")})
	h.mu.Lock()
	inst := h.insts[0]
	h.mu.Unlock()
	inst.stop(context.Background())
	if dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x"}); dec != nil {
		t.Fatalf("down extension contributed %+v", dec)
	}
}

func TestHooks_HK08_InvalidResult(t *testing.T) {
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Env: envWith("ECHO_HOOKS=tool_call", "ECHO_HOOK_BAD=1")})
	if dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x"}); dec != nil {
		t.Fatalf("invalid result contributed %+v", dec)
	}
}

func TestHooks_HK09_LoadOrder(t *testing.T) {
	b := echoDir(t, "bbb", "")
	a := echoDir(t, "aaa", "")
	h := startHost(t, []string{b, a}, HostOptions{})
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.insts[0].manifest.Name != "bbb" || h.insts[1].manifest.Name != "aaa" {
		t.Fatalf("load order = %s, %s", h.insts[0].manifest.Name, h.insts[1].manifest.Name)
	}
}

func TestHooks_HK10_CommandHookBlocks(t *testing.T) {
	dir := commandHookDir(t, "  - on: tool_call\n    run: [\"./hook.sh\"]\n", "#!/bin/sh\ncat >/dev/null\nprintf '{\"block\":true,\"reason\":\"nope\"}'\n")
	h := startHost(t, []string{dir}, HostOptions{})
	dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x", Arguments: json.RawMessage(`{}`)})
	if dec == nil || !dec.Block || dec.Reason != "nope" {
		t.Fatalf("decision = %+v", dec)
	}
}

func TestHooks_HK11_CommandHookRewrites(t *testing.T) {
	dir := commandHookDir(t, "  - on: tool_call\n    run: [\"./hook.sh\"]\n", "#!/bin/sh\ncat >/dev/null\nprintf '{\"arguments\":{\"y\":2}}'\n")
	h := startHost(t, []string{dir}, HostOptions{})
	dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x", Arguments: json.RawMessage(`{}`)})
	if dec == nil || string(dec.Arguments) != `{"y":2}` {
		t.Fatalf("decision = %+v", dec)
	}
}

func TestHooks_HK12_CommandHookNoOpinion(t *testing.T) {
	cases := []string{
		"#!/bin/sh\ncat >/dev/null\n",
		"#!/bin/sh\ncat >/dev/null\nexit 1\n",
		"#!/bin/sh\ncat >/dev/null\necho garbage\n",
	}
	for _, script := range cases {
		dir := commandHookDir(t, "  - on: tool_call\n    run: [\"./hook.sh\"]\n", script)
		h := startHost(t, []string{dir}, HostOptions{})
		if dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "x", Arguments: json.RawMessage(`{}`)}); dec != nil {
			t.Fatalf("script %q contributed %+v", script, dec)
		}
	}
}

func TestHooks_HK13_CommandHookTimeout(t *testing.T) {
	// The script's child inherits the stdout pipe, so killing only the shell
	// would leave the call waiting for the child's full sleep.
	dir := commandHookDir(t, "  - on: tool_call\n    run: [\"./hook.sh\"]\n", "#!/bin/sh\ncat >/dev/null\nsleep 30\n")
	h := startHost(t, []string{dir}, HostOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan *agent.CallDecision, 1)
	go func() {
		done <- h.Hooks().ToolCall(ctx, agent.ToolCall{Name: "x", Arguments: json.RawMessage(`{}`)})
	}()
	select {
	case dec := <-done:
		if dec != nil {
			t.Fatalf("timed-out hook contributed %+v", dec)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a timed-out command hook was not killed: the call is still waiting on its descendants")
	}
}

func TestHooks_HK13b_CommandHookOutputIsCapped(t *testing.T) {
	out, code, err := runHookScript(context.Background(), t.TempDir(),
		[]string{"sh", "-c", "head -c 3000000 /dev/zero | tr '\\000' a"}, nil, commandHookTimeout)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if len(out) == 0 || len(out) > 2*maxHookOutputBytes {
		t.Fatalf("captured %d bytes, want between 1 and %d", len(out), 2*maxHookOutputBytes)
	}
}

func TestHooks_HK14_MatchFilters(t *testing.T) {
	dir := commandHookDir(t, "  - on: tool_call\n    match: {tool: bash}\n    run: [\"./hook.sh\"]\n", "#!/bin/sh\ncat >/dev/null\nprintf '{\"block\":true,\"reason\":\"m\"}'\n")
	h := startHost(t, []string{dir}, HostOptions{})
	if dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "read", Arguments: json.RawMessage(`{}`)}); dec != nil {
		t.Fatalf("non-matching call triggered: %+v", dec)
	}
	if dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "bash", Arguments: json.RawMessage(`{}`)}); dec == nil || !dec.Block {
		t.Fatalf("matching call not triggered: %+v", dec)
	}
}

func TestHooks_HK15_EventJSON(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "event.json")
	script := "#!/bin/sh\ncat > " + seen + "\n"
	dir := commandHookDir(t, "  - on: tool_call\n    run: [\"./hook.sh\"]\n", script)
	h := startHost(t, []string{dir}, HostOptions{})
	_ = h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "bash", ID: "c1", Arguments: json.RawMessage(`{"command":"ls"}`)})
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	var ev struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		CallID    string          `json:"call_id"`
	}
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatalf("event = %s", b)
	}
	if ev.Tool != "bash" || ev.CallID != "c1" || !strings.Contains(string(ev.Arguments), "ls") {
		t.Fatalf("event = %s", b)
	}
}

func TestHooks_HK16_NoShell(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "event.json")
	script := "#!/bin/sh\ncat > " + seen + "\n"
	dir := commandHookDir(t, "  - on: tool_call\n    run: [\"./hook.sh\"]\n", script)
	h := startHost(t, []string{dir}, HostOptions{})
	arg := "a; rm -rf / $(whoami)"
	raw, _ := json.Marshal(map[string]string{"command": arg})
	_ = h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "bash", Arguments: raw})
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "rm -rf /") || !strings.Contains(string(b), "$(whoami)") {
		t.Fatalf("argument was altered: %s", b)
	}
}
