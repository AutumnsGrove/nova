package ext

import (
	"context"
	"math/rand"
	"testing"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// FuzzHostFrames feeds arbitrary frames to the host's dispatcher. Nothing may
// panic, whatever an extension sends.
func FuzzHostFrames(f *testing.F) {
	f.Add("register", []byte(`{"tools":[{"name":"x","parameters":{"type":"object"}}]}`))
	f.Add("ui_request", []byte(`{"method":"confirm","id":"1"}`))
	f.Add("append_entry", []byte(`{"kind":"k","data":{},"keep":true}`))
	f.Add("complete", []byte(`{"id":"m","request":{}}`))
	f.Add("tool_result", []byte(`{"id":"t"}`))
	f.Add("log", []byte(`{"level":"info","text":"hi"}`))
	f.Add("", []byte(`not json`))
	f.Add("tool_call", []byte(`{}`))
	f.Fuzz(func(t *testing.T, typ string, data []byte) {
		h := NewHost(HostOptions{
			Session:  &fakeSession{},
			UI:       &fakeUI{},
			Complete: func(context.Context, provider.Request) (*provider.Response, error) { return nil, nil },
		})
		inst := newInstance(h, &Manifest{
			Name:         "fuzz",
			Capabilities: []string{"tools", "ui:confirm", "session:append_entry", "model:complete"},
		})
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("handleFrame(%q, %q) panicked: %v", typ, data, rec)
			}
		}()
		h.handleFrame(inst, nil, typ, data, reply{Type: typ})
	})
}

// TestProperty_HK_Compose checks that composing chain hooks is associative:
// composing {A} then {B,C} gives the same result as {A,B} then {C} for random
// rewrite values. The hook chain applies handlers in load order, so both
// partitions must equal the full A,B,C composition.
func TestProperty_HK_Compose(t *testing.T) {
	rng := rand.New(rand.NewSource(20261010))
	for i := 0; i < 3; i++ {
		vals := map[string]string{"a": token(rng), "b": token(rng), "c": token(rng)}
		orig := tool.Result{Content: token(rng)}
		full := composedResult(t, []string{"a", "b", "c"}, vals, orig)
		left := composedResult(t, []string{"b", "c"}, vals, tool.Result{Content: composedResult(t, []string{"a"}, vals, orig)})
		right := composedResult(t, []string{"c"}, vals, tool.Result{Content: composedResult(t, []string{"a", "b"}, vals, orig)})
		if full != left || full != right {
			t.Fatalf("composition not associative: full=%q left=%q right=%q (values %v)", full, left, right, vals)
		}
	}
}

// composedResult starts a host for names and returns its tool_result chain
// applied to in.
func composedResult(t *testing.T, names []string, vals map[string]string, in tool.Result) string {
	t.Helper()
	var dirs []string
	env := []string{"ECHO_HOOKS=tool_result"}
	for _, n := range names {
		dirs = append(dirs, echoDir(t, n, ""))
		env = append(env, "ECHO_HOOK_RESULT_"+n+"="+vals[n])
	}
	h := startHost(t, dirs, HostOptions{Env: envWith(env...)})
	defer h.Shutdown(context.Background())
	return h.Hooks().ToolResult(context.Background(), agent.ToolCall{Name: "x"}, in).Content
}

// token returns a short random ASCII string.
func token(rng *rand.Rand) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(b)
}
