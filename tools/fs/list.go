package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/AutumnsGrove/nova/tool"
)

// List returns the list tool. It lists one directory, optionally filtered by a
// glob over entry names, and shows symlinks with their targets.
func List() tool.Tool {
	return tool.New(tool.Info{
		Name:        "list",
		Description: "List files and directories at a path, with size and modification time. Optionally filter by a glob pattern (for example \"*.go\"). Symlinks show their target.",
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
			return tool.Errorf("%v", err), nil
		}
		if in.Pattern != "" {
			if _, err := path.Match(in.Pattern, ""); err != nil {
				return tool.Errorf("invalid pattern: %v", err), nil
			}
		}
		info, err := os.Stat(abs)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		if !info.IsDir() {
			return tool.Errorf("%s is not a directory", abs), nil
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}

		var b strings.Builder
		listed, omitted := 0, 0
		for _, entry := range entries {
			name := entry.Name()
			if in.Pattern != "" {
				ok, _ := path.Match(in.Pattern, name)
				if !ok {
					continue
				}
			}
			if listed >= maxListEntries {
				omitted++
				continue
			}
			b.WriteString(listRow(abs, entry))
			listed++
		}
		if listed == 0 {
			return tool.Text("(no entries)"), nil
		}
		content := b.String()
		if omitted > 0 {
			fmt.Fprintf(&b, "... (%d more entries omitted)\n", omitted)
			content = b.String()
		}
		return tool.Result{Content: content, Truncated: omitted > 0}, nil
	})
}

// listRow renders one directory entry. A symlink resolves its target for size
// and time, and is shown as `name -> target`; an entry that cannot be stat'd is
// shown with `?` rather than dropped.
func listRow(dir string, entry os.DirEntry) string {
	name := entry.Name()
	info, err := entry.Info()
	if entry.Type()&os.ModeSymlink != 0 {
		target, linkErr := os.Readlink(filepath.Join(dir, name))
		if linkErr != nil {
			target = "?"
		}
		full := name + " -> " + target
		if resolved, statErr := os.Stat(filepath.Join(dir, name)); statErr == nil {
			return fmt.Sprintf("%-4s %10s  %s  %s\n", "link", fmt.Sprintf("%d", resolved.Size()), resolved.ModTime().Format("2006-01-02 15:04"), full)
		}
		return fmt.Sprintf("%-4s %10s  %s  %s\n", "link", "?", "?", full)
	}
	if err != nil {
		return fmt.Sprintf("%-4s %10s  %s  %s\n", "file", "?", "?", name)
	}
	kind := "file"
	size := fmt.Sprintf("%d", info.Size())
	if info.IsDir() {
		kind, size, name = "dir", "-", name+"/"
	}
	return fmt.Sprintf("%-4s %10s  %s  %s\n", kind, size, info.ModTime().Format("2006-01-02 15:04"), name)
}
