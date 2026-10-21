package ext

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// A manifest may hold exec tools and a process at once; the process's register
// frame must not take the exec tools away, and a crash must not either.
func TestHost_HST07b_DeclarativeToolsSurviveRegisterAndCrash(t *testing.T) {
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	reg := tool.NewRegistry()
	dir := echoDir(t, "mixed", "tools:\n  - name: decl\n    description: d\n    parameters: {type: object}\n    exec: [echo, hi]\n")
	h := rawHost(t, HostOptions{Dirs: []string{dir}, Registry: reg, Clock: clk, Env: envWith("ECHO_EXIT_AFTER=1")})
	if errs := h.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("start: %v", errs)
	}
	for _, name := range []string{"decl", "echo"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("tool %q missing after register: %v", name, reg.Names())
		}
	}
	_ = reg.Execute(context.Background(), "echo", nil, &tool.Context{})
	waitFor(t, func() bool { _, ok := reg.Get("echo"); return !ok }, "the crash removed the process tool")
	if _, ok := reg.Get("decl"); !ok {
		t.Fatal("a process crash unregistered a declarative tool")
	}
}

func TestHost_HST05b_InvalidProcessToolsAreRefused(t *testing.T) {
	cases := map[string]struct{ names, params string }{
		"name with a space":     {names: "bad name"},
		"name over 64 chars":    {names: strings.Repeat("a", 65)},
		"non-object parameters": {names: "arr", params: `{"type":"array"}`},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			reg := tool.NewRegistry()
			dir := echoDir(t, "echo", "")
			env := []string{"ECHO_TOOL_NAMES=" + tc.names}
			if tc.params != "" {
				env = append(env, "ECHO_TOOL_PARAMS="+tc.params)
			}
			startHost(t, []string{dir}, HostOptions{Registry: reg, Env: envWith(env...)})
			if names := reg.Names(); len(names) != 0 {
				t.Fatalf("registered %v, want none", names)
			}
		})
	}
}

func TestHost_HST05c_RegistrationCountIsCapped(t *testing.T) {
	var names []string
	for i := 0; i < maxRegistrations+10; i++ {
		names = append(names, "t"+strings.Repeat("x", 3)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	startHost(t, []string{dir}, HostOptions{Registry: reg, Env: envWith("ECHO_TOOL_NAMES=" + strings.Join(names, ","))})
	if got := len(reg.Names()); got != maxRegistrations {
		t.Fatalf("registered %d tools, want the cap of %d", got, maxRegistrations)
	}
}
