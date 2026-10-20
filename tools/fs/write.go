package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AutumnsGrove/nova/tool"
)

// Write returns the write tool. It creates parent directories, preserves the
// mode of an existing file, writes through a symlink, and replaces the file
// atomically so a failure cannot truncate it.
func Write() tool.Tool {
	return tool.New(tool.Info{
		Name:        "write",
		Description: "Write content to a file, creating parent directories and overwriting the file if it exists.",
		Destructive: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    tool.StringProp("File path (relative to the working directory, or absolute)"),
				"content": tool.StringProp("Full content to write"),
			},
			"required": []string{"path", "content"},
		},
	}, func(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if len(in.Content) > maxFileBytes {
			return tool.Errorf("content is %d bytes, over the %d byte limit", len(in.Content), maxFileBytes), nil
		}
		abs, err := tool.ResolvePath(tctx, in.Path)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		unlock := lockPath(abs)
		defer unlock()

		if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
			return tool.Errorf("%s is a directory", abs), nil
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return tool.Errorf("%v", err), nil
		}
		target, err := realTarget(abs)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		_, statErr := os.Stat(target)
		created := os.IsNotExist(statErr)
		if err := writeAtomic(target, []byte(in.Content), fileMode(target, 0o644)); err != nil {
			return tool.Errorf("%v", err), nil
		}
		verb := "overwrote"
		if created {
			verb = "created"
		}
		details, _ := json.Marshal(map[string]any{"path": abs, "created": created, "bytes": len(in.Content)})
		return tool.Result{
			Content: fmt.Sprintf("wrote %d bytes to %s (%s)", len(in.Content), abs, verb),
			Details: details,
		}, nil
	})
}
