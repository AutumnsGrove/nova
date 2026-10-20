package ext

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/tool"
)

// commandEnvOpts is a host whose environment holds one scrubbed credential, one
// scrubbed credential the user passes through explicitly, and an ordinary
// variable.
func commandEnvOpts() HostOptions {
	return HostOptions{
		Env:      envWith("NOVA_T_SECRET=hunter2", "NOVA_T_PASSED=through", "NOVA_T_PLAIN=ok"),
		ScrubEnv: []string{"NOVA_T_SECRET", "NOVA_T_PASSED"},
		PassEnv:  []string{"NOVA_T_PASSED"},
	}
}

func TestCommandEnv_HostFiltersOnlyTheScrubbedNames(t *testing.T) {
	// The environment is never printed: it holds whatever the machine's is.
	got := NewHost(commandEnvOpts()).commandEnv()
	if slices.Contains(got, "NOVA_T_SECRET=hunter2") {
		t.Error("the scrubbed credential reached a command")
	}
	for _, want := range []string{"NOVA_T_PASSED=through", "NOVA_T_PLAIN=ok"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is missing from the command environment", want)
		}
	}
}

func TestCommandEnv_RunExecUsesTheGivenEnvironment(t *testing.T) {
	out := runExec(context.Background(), t.TempDir(), []string{"sh", "-c", `echo "[$NOVA_T_SECRET][$NOVA_T_PLAIN]"`}, 10*time.Second, []string{"NOVA_T_PLAIN=ok"})
	if got := strings.TrimSpace(out.content); got != "[][ok]" {
		t.Fatalf("runExec saw %q", got)
	}
}

func TestCommandEnv_HookScriptUsesTheGivenEnvironment(t *testing.T) {
	out, code, err := runHookScript(context.Background(), t.TempDir(), []string{"sh", "-c", `echo "[$NOVA_T_SECRET]"`}, nil, commandHookTimeout, []string{"NOVA_T_PLAIN=ok"})
	if err != nil || code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("hook saw %q (code %d, err %v)", out, code, err)
	}
}

func TestCommandEnv_BuildRunnerUsesTheGivenEnvironment(t *testing.T) {
	// `test -z` fails the step if the secret is visible.
	run := ExecRunner{Env: []string{"NOVA_T_PLAIN=ok"}}
	if err := run.Run(context.Background(), t.TempDir(), []string{"sh", "-c", `test -z "$NOVA_T_SECRET" && test "$NOVA_T_PLAIN" = ok`}); err != nil {
		t.Fatalf("build step: %v", err)
	}
}

// The declarative tool and the command hook both run through the host, so they
// get its filtered environment without any extension doing anything.
func TestCommandEnv_ThroughARealHost(t *testing.T) {
	t.Setenv("NOVA_T_SECRET", "hunter2")
	dir := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\nprintf '{\"block\":true,\"reason\":\"[%s]\"}' \"$NOVA_T_SECRET\"\n"
	if err := os.WriteFile(filepath.Join(dir, "hook.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: cmdenv\nversion: 1.0.0\ndescription: x\n" +
		"tools:\n  - name: peek\n    description: x\n    parameters: {type: object}\n" +
		"    exec: [\"sh\", \"-c\", \"echo [$NOVA_T_SECRET]\"]\n" +
		"hooks:\n  - on: tool_call\n    run: [\"./hook.sh\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	opt := commandEnvOpts()
	opt.Env = os.Environ
	h := startHost(t, []string{dir}, opt)

	tl, ok := h.Registry().Get("peek")
	if !ok {
		t.Fatal("the declarative tool was not registered")
	}
	res, err := tl.Run(context.Background(), json.RawMessage(`{}`), &tool.Context{WorkDir: dir})
	if err != nil || strings.TrimSpace(res.Content) != "[]" {
		t.Fatalf("declarative tool saw %+v, %v", res, err)
	}
	dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "read", Arguments: json.RawMessage(`{}`)})
	if dec == nil || dec.Reason != "[]" {
		t.Fatalf("command hook saw %+v", dec)
	}
}
