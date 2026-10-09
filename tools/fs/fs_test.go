package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/tool"
)

func TestWriteEditReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tctx := &tool.Context{WorkDir: dir}
	ctx := context.Background()

	write := Write()
	if _, err := write.Run(ctx, json.RawMessage(`{"path":"notes/a.txt","content":"hello world"}`), tctx); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes", "a.txt")); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	edit := Edit()
	res, err := edit.Run(ctx, json.RawMessage(`{"path":"notes/a.txt","edits":[{"oldText":"world","newText":"nova"}]}`), tctx)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if res.IsError {
		t.Fatalf("edit error result: %+v", res)
	}

	read := Read()
	res, err = read.Run(ctx, json.RawMessage(`{"path":"notes/a.txt"}`), tctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(res.Content, "hello nova") {
		t.Fatalf("read content = %q, want it to contain 'hello nova'", res.Content)
	}
	if !strings.Contains(res.Content, "1\thello nova") {
		t.Fatalf("read content is not line-numbered: %q", res.Content)
	}
}

func TestEditRejectsNonUniqueOldText(t *testing.T) {
	dir := t.TempDir()
	tctx := &tool.Context{WorkDir: dir}
	ctx := context.Background()

	write := Write()
	if _, err := write.Run(ctx, json.RawMessage(`{"path":"dup.txt","content":"x x"}`), tctx); err != nil {
		t.Fatalf("write: %v", err)
	}

	edit := Edit()
	res, err := edit.Run(ctx, json.RawMessage(`{"path":"dup.txt","edits":[{"oldText":"x","newText":"y"}]}`), tctx)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "unique") {
		t.Fatalf("expected uniqueness error, got %+v", res)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	tctx := &tool.Context{WorkDir: dir}
	ctx := context.Background()

	write := Write()
	_, _ = write.Run(ctx, json.RawMessage(`{"path":"a.txt","content":"a"}`), tctx)
	_, _ = write.Run(ctx, json.RawMessage(`{"path":"b.go","content":"b"}`), tctx)

	list := List()
	res, err := list.Run(ctx, json.RawMessage(`{"path":".","pattern":"*.go"}`), tctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(res.Content, "b.go") || strings.Contains(res.Content, "a.txt") {
		t.Fatalf("list content = %q, want only b.go", res.Content)
	}
}
