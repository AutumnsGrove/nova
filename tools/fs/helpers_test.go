package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/AutumnsGrove/nova/tool"
)

// run marshals args, runs the tool, and fails on a Go error. A tool-level
// failure is an error Result, not a Go error.
func run(t *testing.T, tl tool.Tool, tctx *tool.Context, args any) tool.Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := tl.Run(context.Background(), raw, tctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// runRaw is run for callers that already have JSON arguments.
func runRaw(t *testing.T, tl tool.Tool, tctx *tool.Context, args string) (tool.Result, error) {
	t.Helper()
	return tl.Run(context.Background(), json.RawMessage(args), tctx)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
