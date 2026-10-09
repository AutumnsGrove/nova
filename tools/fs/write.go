package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AutumnsGrove/nova/tool"
)

// Write returns the write tool. It creates parent directories and overwrites
// the target if it already exists.
func Write() tool.Tool {
	return tool.New(tool.Info{
		Name:        "write",
		Description: "Write content to a file, creating parent directories and overwriting the file if it exists.",
		Destructive: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    stringProp("File path (relative to the working directory, or absolute)"),
				"content": stringProp("Full content to write"),
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
		abs, err := resolve(tctx, in.Path)
		if err != nil {
			return tool.Result{}, err
		}
		if dir := filepath.Dir(abs); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return tool.Errorf("%v", err), nil
			}
		}
		if err := os.WriteFile(abs, []byte(in.Content), 0o644); err != nil {
			return tool.Errorf("%v", err), nil
		}
		return tool.Text("wrote %d bytes to %s", len(in.Content), abs), nil
	})
}
