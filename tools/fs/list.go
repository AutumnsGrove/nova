package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AutumnsGrove/nova/tool"
)

// List returns the list tool. It lists one directory, optionally filtered by a
// glob pattern on the entry name.
func List() tool.Tool {
	return tool.New(tool.Info{
		Name:        "list",
		Description: "List files and directories at a path, with size and modification time. Optionally filter by a glob pattern (for example \"*.go\").",
		ReadOnly:    true,
		Parallel:    true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    stringProp("Directory path (default \".\")"),
				"pattern": stringProp("Optional glob pattern matched against entry names"),
			},
		},
	}, func(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		var in struct {
			Path    string `json:"path"`
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.Path == "" {
			in.Path = "."
		}
		abs, err := resolve(tctx, in.Path)
		if err != nil {
			return tool.Result{}, err
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		type row struct {
			name  string
			size  int64
			mtime string
			isDir bool
		}
		var rows []row
		for _, entry := range entries {
			if in.Pattern != "" {
				ok, matchErr := filepath.Match(in.Pattern, entry.Name())
				if matchErr != nil {
					return tool.Errorf("invalid pattern: %v", matchErr), nil
				}
				if !ok {
					continue
				}
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			rows = append(rows, row{
				name:  entry.Name(),
				size:  info.Size(),
				mtime: info.ModTime().Format("2006-01-02 15:04"),
				isDir: entry.IsDir(),
			})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

		var b strings.Builder
		for _, r := range rows {
			kind := "file"
			if r.isDir {
				kind = "dir "
			}
			size := fmt.Sprintf("%d", r.size)
			if r.isDir {
				size = "-"
			}
			fmt.Fprintf(&b, "%-4s %10s  %s  %s\n", kind, size, r.mtime, r.name)
		}
		if b.Len() == 0 {
			return tool.Text("(no entries)"), nil
		}
		return tool.Result{Content: b.String()}, nil
	})
}
