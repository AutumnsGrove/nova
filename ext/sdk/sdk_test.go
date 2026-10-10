package sdk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// peer drives a Run in process over pipes.
type peer struct {
	t    *testing.T
	inW  *io.PipeWriter
	outR *bufio.Reader
	done chan error
}

// startSDK runs fn behind pipes and reads the register frame.
func startSDK(t *testing.T, fn func(*Host) error) *peer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &peer{t: t, inW: inW, outR: bufio.NewReader(outR), done: make(chan error, 1)}
	go func() { p.done <- run(fn, inR, outW) }()
	p.send(map[string]any{"type": "init"})
	p.recv() // register
	t.Cleanup(func() {
		_ = inW.Close()
		_ = outW.Close()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
		}
	})
	return p
}

func (p *peer) send(v any) {
	p.t.Helper()
	b, _ := json.Marshal(v)
	if _, err := p.inW.Write(append(b, '\n')); err != nil {
		p.t.Fatalf("send: %v", err)
	}
}

func (p *peer) recv() map[string]any {
	p.t.Helper()
	line, err := p.outR.ReadBytes('\n')
	if err != nil {
		p.t.Fatalf("recv: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		p.t.Fatalf("recv %q: %v", line, err)
	}
	return m
}

func TestSDK_SDK01_Handshake(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.Tool(Tool{Name: "hello", Description: "d", Params: Object(String("name", "n"))}, func(*Call) (Result, error) {
			return Text("hi"), nil
		})
		return nil
	})
	_ = p
}

func TestSDK_SDK02_ToolHandler(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.Tool(Tool{Name: "echo"}, func(c *Call) (Result, error) {
			return Text("got %s", c.String("x")), nil
		})
		return nil
	})
	p.send(map[string]any{"type": "tool_call", "id": "t1", "name": "echo", "arguments": map[string]any{"x": "y"}})
	got := p.recv()
	if got["content"] != "got y" || got["is_error"] == true {
		t.Fatalf("result = %+v", got)
	}
}

func TestSDK_SDK03_HandlerError(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.Tool(Tool{Name: "bad"}, func(*Call) (Result, error) {
			return Result{}, io.ErrUnexpectedEOF
		})
		return nil
	})
	p.send(map[string]any{"type": "tool_call", "id": "t1", "name": "bad"})
	got := p.recv()
	if got["is_error"] != true {
		t.Fatalf("result = %+v", got)
	}
}

func TestSDK_SDK04_HandlerPanic(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.Tool(Tool{Name: "boom"}, func(*Call) (Result, error) { panic("x") })
		return nil
	})
	p.send(map[string]any{"type": "tool_call", "id": "t1", "name": "boom"})
	got := p.recv()
	if got["is_error"] != true {
		t.Fatalf("result = %+v", got)
	}
	p.send(map[string]any{"type": "tool_call", "id": "t2", "name": "boom"})
	if p.recv()["is_error"] != true {
		t.Fatal("process did not survive the panic")
	}
}

func TestSDK_SDK05_Deadline(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.Tool(Tool{Name: "d"}, func(c *Call) (Result, error) {
			dl, ok := c.Context().Deadline()
			if !ok {
				return Text("no deadline"), nil
			}
			return Text("%d", dl.UnixNano()-time.Now().UnixNano()), nil
		})
		return nil
	})
	p.send(map[string]any{"type": "tool_call", "id": "t1", "name": "d", "deadline_ms": 5000})
	got := p.recv()
	ns, _ := got["content"].(string)
	if ns == "no deadline" {
		t.Fatal("deadline missing from the context")
	}
}

func TestSDK_SDK06_HookHandler(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.On("tool_call", func(*Hook) (*HookDecision, error) {
			return &HookDecision{Block: true, Reason: "no"}, nil
		})
		return nil
	})
	p.send(map[string]any{"type": "hook", "id": "h1", "hook": "tool_call", "event": map[string]any{"tool": "x"}})
	got := p.recv()
	res, _ := got["result"].(map[string]any)
	if res["block"] != true || res["reason"] != "no" {
		t.Fatalf("result = %+v", got)
	}
}

func TestSDK_SDK07_UIHelpers(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.Tool(Tool{Name: "ask"}, func(c *Call) (Result, error) {
			if err := c.UI.Notify("hello", "info"); err != nil {
				return Result{}, err
			}
			ok, err := c.UI.Confirm(c.Context(), "t", "m")
			return Text("%v", ok), err
		})
		return nil
	})
	p.send(map[string]any{"type": "tool_call", "id": "t1", "name": "ask"})
	first := p.recv()
	if first["method"] != "notify" {
		t.Fatalf("first frame = %+v", first)
	}
	second := p.recv()
	if second["method"] != "confirm" {
		t.Fatalf("second frame = %+v", second)
	}
	id, _ := second["id"].(string)
	p.send(map[string]any{"type": "ui_response", "id": id, "ok": true})
	if got := p.recv(); got["content"] != "true" {
		t.Fatalf("result = %+v", got)
	}
}

func TestSDK_SDK08_AppendEntryAndEntries(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.Tool(Tool{Name: "read"}, func(*Call) (Result, error) {
			return Text("%d", len(h.Entries("todo.state"))), nil
		})
		return nil
	})
	p.send(map[string]any{"type": "replay", "kind": "todo.state", "data": map[string]any{"n": 1}})
	p.send(map[string]any{"type": "replay", "kind": "todo.state", "data": map[string]any{"n": 2}})
	p.send(map[string]any{"type": "tool_call", "id": "t1", "name": "read"})
	if got := p.recv(); got["content"] != "2" {
		t.Fatalf("result = %+v", got)
	}
}

func TestSDK_SDK09_Complete(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.Tool(Tool{Name: "c"}, func(c *Call) (Result, error) {
			raw, err := h.Complete(c.Context(), map[string]any{"Model": "x"})
			if err != nil {
				return Result{}, err
			}
			return Text("%s", raw), nil
		})
		return nil
	})
	p.send(map[string]any{"type": "tool_call", "id": "t1", "name": "c"})
	frame := p.recv()
	if frame["type"] != "complete" {
		t.Fatalf("frame = %+v", frame)
	}
	id, _ := frame["id"].(string)
	p.send(map[string]any{"type": "complete_result", "id": id, "message": map[string]any{"role": "assistant"}})
	if got := p.recv(); got["type"] != "tool_result" {
		t.Fatalf("result = %+v", got)
	}
}

func TestSDK_SDK10_CleanExit(t *testing.T) {
	p := startSDK(t, func(*Host) error { return nil })
	p.send(map[string]any{"type": "shutdown"})
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatalf("shutdown returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on shutdown")
	}
}

func TestSDK_SDK10_EOFExit(t *testing.T) {
	p := startSDK(t, func(*Host) error { return nil })
	_ = p.inW.Close()
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on stdin EOF")
	}
}

func TestSDK_SDK11_SchemaHelpers(t *testing.T) {
	schema := Object(String("name", "Who", Required), Int("count", "How many"), Bool("flag", "yes"))
	if err := tool.ValidateArgs(schema, json.RawMessage(`{"name":"x","count":2,"flag":true}`)); err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
	if err := tool.ValidateArgs(schema, json.RawMessage(`{"count":2}`)); err == nil {
		t.Fatal("missing required property accepted")
	}
}

func TestSDK_SDK12_Concurrent(t *testing.T) {
	p := startSDK(t, func(h *Host) error {
		h.Tool(Tool{Name: "slow"}, func(c *Call) (Result, error) {
			time.Sleep(5 * time.Millisecond)
			return Text("ok"), nil
		})
		return nil
	})
	for i := 0; i < 20; i++ {
		p.send(map[string]any{"type": "tool_call", "id": fmt.Sprintf("t%d", i), "name": "slow"})
	}
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		seen[toJSON(p.recv()["id"])] = true
	}
	if len(seen) != 20 {
		t.Fatalf("distinct ids = %d, want 20", len(seen))
	}
}

// toJSON renders a decoded value so ids compare by string.
func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
