package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// toolByName builds a tool named name whose Run calls fn.
func toolByName(name string, fn Func) Tool {
	return New(Info{Name: name, Description: name + " tool"}, fn)
}

// echoTool returns a tool that echoes its name.
func echoTool(name string) Tool {
	return toolByName(name, func(context.Context, json.RawMessage, *Context) (Result, error) {
		return Text("ran %s", name), nil
	})
}

// mustRegister registers a tool or fails the test.
func mustRegister(t *testing.T, r *Registry, tool Tool) {
	t.Helper()
	if err := r.Register(tool); err != nil {
		t.Fatalf("register %s: %v", tool.Info().Name, err)
	}
}

// TestRegistry_REG01_RegisterThenExecute checks the happy path.
func TestRegistry_REG01_RegisterThenExecute(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	res := r.Execute(context.Background(), "a", nil, nil)
	if res.IsError || res.Content != "ran a" {
		t.Fatalf("Execute = %+v", res)
	}
}

// TestRegistry_REG02_UnknownTool checks the error names the tool and lists up
// to ten known tools.
func TestRegistry_REG02_UnknownTool(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 12; i++ {
		mustRegister(t, r, echoTool(fmt.Sprintf("t%02d", i)))
	}
	res := r.Execute(context.Background(), "nope", nil, nil)
	if !res.IsError {
		t.Fatalf("unknown tool returned success: %+v", res)
	}
	if !strings.Contains(res.Content, "nope") {
		t.Errorf("content does not name the tool: %q", res.Content)
	}
	if !strings.Contains(res.Content, "t00") {
		t.Errorf("content does not list a known tool: %q", res.Content)
	}
	if strings.Contains(res.Content, "t11") {
		t.Errorf("content lists more than ten tools: %q", res.Content)
	}
}

// TestRegistry_REG03_Duplicate checks a duplicate name errors.
func TestRegistry_REG03_Duplicate(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	if err := r.Register(echoTool("a")); err == nil {
		t.Fatal("duplicate registration succeeded")
	}
}

// TestRegistry_REG04_NilAndEmpty checks nil and unnamed tools are rejected.
func TestRegistry_REG04_NilAndEmpty(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("nil tool registered")
	}
	if err := r.Register(toolByName("", nil)); err == nil {
		t.Error("empty name registered")
	}
}

// TestRegistry_REG05_ReplacePosition checks Replace keeps the tool's position.
func TestRegistry_REG05_ReplacePosition(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	mustRegister(t, r, echoTool("b"))
	mustRegister(t, r, echoTool("c"))
	if err := r.Replace(toolByName("b", func(context.Context, json.RawMessage, *Context) (Result, error) {
		return Text("new b"), nil
	})); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got := r.Names(); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("Names = %v, want a,b,c", got)
	}
	if res := r.Execute(context.Background(), "b", nil, nil); res.Content != "new b" {
		t.Fatalf("Execute b = %q, want new b", res.Content)
	}
}

// TestRegistry_REG06_ReplaceMissing checks Replace registers a new tool last.
func TestRegistry_REG06_ReplaceMissing(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	if err := r.Replace(echoTool("z")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got := r.Names(); strings.Join(got, ",") != "a,z" {
		t.Fatalf("Names = %v, want a,z", got)
	}
}

// TestRegistry_REG07_Unregister checks removal and its return value.
func TestRegistry_REG07_Unregister(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	mustRegister(t, r, echoTool("b"))
	if !r.Unregister("a") {
		t.Error("Unregister(a) = false, want true")
	}
	if r.Unregister("a") {
		t.Error("Unregister(a) twice = true, want false")
	}
	if got := r.Names(); strings.Join(got, ",") != "b" {
		t.Fatalf("Names = %v, want b", got)
	}
}

// TestRegistry_REG08_InvalidArgs checks failing validation never runs the tool.
func TestRegistry_REG08_InvalidArgs(t *testing.T) {
	var calls atomic.Int32
	r := NewRegistry()
	mustRegister(t, r, New(Info{
		Name:       "w",
		Parameters: map[string]any{"type": "object", "required": []any{"path"}},
	}, func(context.Context, json.RawMessage, *Context) (Result, error) {
		calls.Add(1)
		return Text("ok"), nil
	}))
	res := r.Execute(context.Background(), "w", json.RawMessage(`{}`), nil)
	if !res.IsError || !strings.Contains(res.Content, "invalid arguments") {
		t.Fatalf("Execute = %+v, want invalid arguments", res)
	}
	if calls.Load() != 0 {
		t.Fatalf("tool ran %d times, want 0", calls.Load())
	}
}

// TestRegistry_REG09_EmptyArgs checks empty and null args run an open tool.
func TestRegistry_REG09_EmptyArgs(t *testing.T) {
	var calls atomic.Int32
	r := NewRegistry()
	mustRegister(t, r, toolByName("a", func(context.Context, json.RawMessage, *Context) (Result, error) {
		calls.Add(1)
		return Text("ok"), nil
	}))
	for _, args := range []json.RawMessage{nil, {}, json.RawMessage(`null`)} {
		if res := r.Execute(context.Background(), "a", args, nil); res.IsError {
			t.Fatalf("Execute(%q) = %+v", args, res)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("tool ran %d times, want 3", calls.Load())
	}
}

// TestRegistry_REG10_NonObjectArgs checks arrays, strings, and numbers are
// rejected as arguments.
func TestRegistry_REG10_NonObjectArgs(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	for _, args := range []json.RawMessage{json.RawMessage(`[1]`), json.RawMessage(`"x"`), json.RawMessage(`5`)} {
		if res := r.Execute(context.Background(), "a", args, nil); !res.IsError {
			t.Errorf("Execute(%s) = %+v, want an error", args, res)
		}
	}
}

// TestRegistry_REG11_Cancelled checks a cancelled context never runs the tool.
func TestRegistry_REG11_Cancelled(t *testing.T) {
	var calls atomic.Int32
	r := NewRegistry()
	mustRegister(t, r, toolByName("a", func(context.Context, json.RawMessage, *Context) (Result, error) {
		calls.Add(1)
		return Text("ok"), nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := r.Execute(ctx, "a", nil, nil)
	if !res.IsError || !strings.Contains(res.Content, "cancelled") {
		t.Fatalf("Execute = %+v, want cancelled", res)
	}
	if calls.Load() != 0 {
		t.Fatalf("tool ran %d times, want 0", calls.Load())
	}
}

// TestRegistry_REG12_Panic checks a panicking tool becomes an error result.
func TestRegistry_REG12_Panic(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, toolByName("boom", func(context.Context, json.RawMessage, *Context) (Result, error) {
		panic("kaboom")
	}))
	res := r.Execute(context.Background(), "boom", nil, nil)
	if !res.IsError || !strings.Contains(res.Content, "internal error in boom") {
		t.Fatalf("Execute = %+v, want internal error", res)
	}
}

// TestRegistry_REG13_PanicValue checks non-string panic values are rendered.
func TestRegistry_REG13_PanicValue(t *testing.T) {
	cases := []struct {
		name string
		fn   Func
		want string
	}{
		{"number", func(context.Context, json.RawMessage, *Context) (Result, error) { panic(42) }, "42"},
		{"error", func(context.Context, json.RawMessage, *Context) (Result, error) {
			panic(errors.New("caught"))
		}, "caught"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			mustRegister(t, r, toolByName("p", tc.fn))
			res := r.Execute(context.Background(), "p", nil, nil)
			if !res.IsError || !strings.Contains(res.Content, tc.want) {
				t.Fatalf("Execute = %+v, want %q", res, tc.want)
			}
		})
	}
}

// TestRegistry_REG14_ToolError checks a returned error becomes `error: tool: err`.
func TestRegistry_REG14_ToolError(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, toolByName("boom", func(context.Context, json.RawMessage, *Context) (Result, error) {
		return Result{}, errors.New("failed")
	}))
	res := r.Execute(context.Background(), "boom", nil, nil)
	if !res.IsError || res.Content != "error: boom: failed" {
		t.Fatalf("Execute = %q, want error: boom: failed", res.Content)
	}
}

// TestRegistry_REG15_Concurrent checks Execute and Register are race-free.
func TestRegistry_REG15_Concurrent(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 10; i++ {
		mustRegister(t, r, echoTool(fmt.Sprintf("base%d", i)))
	}
	const goroutines = 50
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				res := r.Execute(context.Background(), "base0", nil, nil)
				if res.IsError {
					t.Errorf("Execute = %+v", res)
				}
				return
			}
			_ = r.Register(echoTool(fmt.Sprintf("new%d", i)))
		}(i)
	}
	wg.Wait()
}

// TestRegistry_REG16_ViewSelects checks View keeps only the named tools.
func TestRegistry_REG16_ViewSelects(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	mustRegister(t, r, echoTool("b"))
	mustRegister(t, r, echoTool("c"))
	view := r.View("a", "c", "missing")
	if got := view.Names(); strings.Join(got, ",") != "a,c" {
		t.Fatalf("view Names = %v, want a,c", got)
	}
}

// TestRegistry_REG17_ViewIndependent checks registering on a view does not
// change the parent.
func TestRegistry_REG17_ViewIndependent(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	view := r.View("a")
	mustRegister(t, view, echoTool("b"))
	if got := r.Names(); strings.Join(got, ",") != "a" {
		t.Fatalf("parent Names = %v, want a", got)
	}
	if _, ok := r.Get("b"); ok {
		t.Error("tool registered on the view leaked to the parent")
	}
}

// TestRegistry_REG18_DefsOrder checks definitions keep registration order.
func TestRegistry_REG18_DefsOrder(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	mustRegister(t, r, echoTool("b"))
	mustRegister(t, r, echoTool("c"))
	var names []string
	for _, d := range r.Defs() {
		names = append(names, d.Function.Name)
	}
	if strings.Join(names, ",") != "a,b,c" {
		t.Fatalf("Defs order = %v, want a,b,c", names)
	}
}

// TestRegistry_REG19_ProgressNilEmit checks Progress is a no-op without Emit.
func TestRegistry_REG19_ProgressNilEmit(t *testing.T) {
	tctx := &Context{}
	tctx.Progress("ignored")
}

// TestRegistry_REG20_ProgressEmit checks Progress delivers an update.
func TestRegistry_REG20_ProgressEmit(t *testing.T) {
	var got []string
	tctx := &Context{Emit: func(u Update) { got = append(got, u.Text) }}
	tctx.Progress("working")
	if len(got) != 1 || got[0] != "working" {
		t.Fatalf("updates = %v, want [working]", got)
	}
}

// TestRegistry_REG21_Info checks the Info lookup hit and miss.
func TestRegistry_REG21_Info(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, echoTool("a"))
	info, ok := r.Info("a")
	if !ok || info.Name != "a" {
		t.Fatalf("Info(a) = (%+v, %v)", info, ok)
	}
	if _, ok := r.Info("missing"); ok {
		t.Error("Info(missing) found a tool")
	}
}

// TestRegistry_REG22_DetailsPassThrough checks structured details survive
// Execute unchanged.
func TestRegistry_REG22_DetailsPassThrough(t *testing.T) {
	details := json.RawMessage(`{"k":1}`)
	r := NewRegistry()
	mustRegister(t, r, toolByName("a", func(context.Context, json.RawMessage, *Context) (Result, error) {
		return Result{Content: "ok", Details: details}, nil
	}))
	res := r.Execute(context.Background(), "a", nil, nil)
	if string(res.Details) != string(details) {
		t.Fatalf("Details = %s, want %s", res.Details, details)
	}
}
