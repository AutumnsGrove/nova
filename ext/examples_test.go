package ext

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/tool"
)

// exampleRoot is the shipped examples directory relative to this package.
func exampleRoot() string { return filepath.Join("..", "examples", "extensions") }

func TestExamples_EXA01_AllShipAndPassCheck(t *testing.T) {
	entries, err := os.ReadDir(exampleRoot())
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		dir := filepath.Join(exampleRoot(), e.Name())
		if !e.IsDir() || !pathExists(filepath.Join(dir, "extension.yaml")) {
			continue
		}
		seen++
		problems := Check(context.Background(), dir, CheckOptions{Timeout: 3 * time.Minute})
		if len(problems) != 0 {
			t.Errorf("example %s: %v", e.Name(), problems)
		}
	}
	if seen != 3 {
		t.Fatalf("found %d examples, want 3", seen)
	}
}

func TestExampleHello_EXA01(t *testing.T) {
	dir := filepath.Join(exampleRoot(), "hello")
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	tl := &execTool{dir: dir, spec: m.Tools[0]}
	res, err := tl.Run(context.Background(), json.RawMessage(`{"name":"World"}`), &tool.Context{WorkDir: dir})
	if err != nil || res.IsError || res.Content != "Hello, World!" {
		t.Fatalf("result = %+v err=%v", res, err)
	}
}

func TestExampleBlockRm_EXA01(t *testing.T) {
	dir := filepath.Join(exampleRoot(), "block-rm")
	h := startHost(t, []string{dir}, HostOptions{})
	dec := h.Hooks().ToolCall(context.Background(), agent.ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"rm -rf /"}`)})
	if dec == nil || !dec.Block {
		t.Fatalf("decision = %+v", dec)
	}
}

func TestExampleHelloGo_EXA01(t *testing.T) {
	dir := filepath.Join(exampleRoot(), "hello-go")
	if problems := Check(context.Background(), dir, CheckOptions{Timeout: 3 * time.Minute}); len(problems) != 0 {
		t.Fatalf("check: %v", problems)
	}
	reg := tool.NewRegistry()
	h := startHost(t, []string{dir}, HostOptions{Registry: reg})
	_ = h
	res := reg.Execute(context.Background(), "hello", json.RawMessage(`{"name":"World"}`), &tool.Context{WorkDir: dir})
	if res.IsError || res.Content != "Hello, World!" {
		t.Fatalf("result = %+v", res)
	}
}
