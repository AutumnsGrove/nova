package search

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

// writeFile writes a file, creating parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// tree builds a set of files under a fresh temp directory. Keys are slash
// paths relative to the root; a trailing slash makes a directory.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", rel, err)
		}
	}
	return dir
}

// mustJSON re-marshals a Details blob into a map for assertions.
func mustJSON(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal details %s: %v", raw, err)
	}
	return m
}
