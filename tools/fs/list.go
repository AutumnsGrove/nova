package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
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
				"path":    tool.StringProp("Directory path (default \".\")"),
				"pattern": tool.StringProp("Optional glob pattern matched against entry names"),
			},
		},
	}, func(_ context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
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
		abs, err := tool.ResolvePath(tctx, in.Path)
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
		if omitted > 0 {
			fmt.Fprintf(&b, "... (%d more entries omitted)\n", omitted)
		}
		return tool.Result{Content: b.String(), Truncated: omitted > 0}, nil
	})
}

// listRow renders one directory entry. A symlink resolves its target for size
// and time, and is shown as `name -> target`; an entry that cannot be stat'd is
// shown with `?` rather than dropped.
func listRow(dir string, entry os.DirEntry) string {
	name := entry.Name()
	full := filepath.Join(dir, name)
	if entry.Type()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			target = "?"
		}
		size, mtime := "?", "?"
		if info, err := os.Stat(full); err == nil {
			size, mtime = strconv.FormatInt(info.Size(), 10), info.ModTime().Format(listTimeLayout)
		}
		return listLine("link", size, mtime, name+" -> "+target)
	}
	kind := "file"
	if entry.IsDir() {
		kind, name = "dir", name+"/"
	}
	info, err := entry.Info()
	if err != nil {
		return listLine(kind, "?", "?", name)
	}
	size := strconv.FormatInt(info.Size(), 10)
	if entry.IsDir() {
		size = "-"
	}
	return listLine(kind, size, info.ModTime().Format(listTimeLayout), name)
}

// listTimeLayout is how list shows a modification time.
const listTimeLayout = "2006-01-02 15:04"

// listLine formats one row with the columns aligned.
func listLine(kind, size, mtime, name string) string {
	return fmt.Sprintf("%-4s %10s  %s  %s\n", kind, size, mtime, name)
}
