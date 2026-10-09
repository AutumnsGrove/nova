package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/AutumnsGrove/nova/tool"
)

// Read returns the read tool. It reads a UTF-8 text file and returns
// line-numbered content, which makes follow-up edits unambiguous.
func Read() tool.Tool {
	return tool.New(tool.Info{
		Name:        "read",
		Description: "Read a UTF-8 text file and return its contents with line numbers. Use offset and limit to page through large files.",
		ReadOnly:    true,
		Parallel:    true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   stringProp("File path (relative to the working directory, or absolute)"),
				"offset": intProp("1-indexed line to start from (default 1)"),
				"limit":  intProp("Maximum number of lines to read (default 2000)"),
			},
			"required": []string{"path"},
		},
	}, func(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		var in struct {
			Path   string `json:"path"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		abs, err := resolve(tctx, in.Path)
		if err != nil {
			return tool.Result{}, err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		if isProbablyBinary(data) {
			return tool.Errorf("%s looks like a binary file (%d bytes)", abs, len(data)), nil
		}

		lines := strings.Split(string(data), "\n")
		start := 0
		if in.Offset > 1 {
			start = in.Offset - 1
		}
		if start > len(lines) {
			start = len(lines)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 2000
		}
		end := start + limit
		if end > len(lines) {
			end = len(lines)
		}

		var b strings.Builder
		for i := start; i < end; i++ {
			fmt.Fprintf(&b, "%d\t%s\n", i+1, lines[i])
		}
		out := b.String()
		if end < len(lines) {
			out += fmt.Sprintf("\n... (%d more lines, continue with offset=%d)\n", len(lines)-end, end+1)
		}
		if out == "" {
			out = "(empty file)"
		}
		return tool.Result{Content: out}, nil
	})
}

// isProbablyBinary reports whether data contains a NUL byte in its first block,
// which is a cheap and reliable text/binary heuristic.
func isProbablyBinary(data []byte) bool {
	const window = 8000
	if len(data) > window {
		data = data[:window]
	}
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}
