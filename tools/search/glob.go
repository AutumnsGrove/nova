package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/tool"
)

// maxGlobResults caps one glob call's output.
const maxGlobResults = 1000

// globDetails is the structured data glob attaches to Result.Details.
type globDetails struct {
	Count     int  `json:"count"`
	Truncated bool `json:"truncated"`
}

// Glob returns the glob tool. It searches file names with `**` patterns and
// returns paths relative to the search directory.
func Glob() tool.Tool {
	return tool.New(tool.Info{
		Name:        "glob",
		Description: "Find files by name using a glob pattern (for example \"**/*.go\"). Results are relative paths, sorted, and capped.",
		ReadOnly:    true,
		Parallel:    true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":   stringProp("Glob pattern; `**` matches across directories, `*` and `?` do not. A trailing `/` matches directories only."),
				"path":      stringProp("Directory to search (default \".\")"),
				"hidden":    boolProp("Include dot-files and dot-directories"),
				"no_ignore": boolProp("Ignore .gitignore files"),
			},
			"required": []string{"pattern"},
		},
	}, func(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		var in struct {
			Pattern  string `json:"pattern"`
			Path     string `json:"path"`
			Hidden   bool   `json:"hidden"`
			NoIgnore bool   `json:"no_ignore"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.Pattern == "" {
			return tool.Errorf("pattern is required"), nil
		}
		dirsOnly := strings.HasSuffix(in.Pattern, "/")
		base := strings.TrimSuffix(in.Pattern, "/")
		if base == "" || !globValid(base) {
			return tool.Errorf("invalid pattern: %s", in.Pattern), nil
		}
		if in.Path == "" {
			in.Path = "."
		}
		root, err := resolve(tctx, in.Path)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		info, err := os.Stat(root)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		if !info.IsDir() {
			return tool.Errorf("%s is not a directory", in.Path), nil
		}

		var stats walkStats
		var matches []string
		opt := walkOptions{NoIgnore: in.NoIgnore, Hidden: in.Hidden, stats: &stats}
		err = walk(ctx, root, opt, func(e walkEntry) error {
			if e.IsDir != dirsOnly {
				return nil
			}
			if globMatch(base, e.Rel) {
				matches = append(matches, e.Rel)
			}
			return nil
		})
		if err != nil {
			return tool.Errorf("%v", err), nil
		}

		sort.Strings(matches)
		truncated := len(matches) > maxGlobResults
		omitted := 0
		if truncated {
			omitted = len(matches) - maxGlobResults
			matches = matches[:maxGlobResults]
		}
		if len(matches) == 0 && stats.unreadable == 0 {
			return tool.Text("no files match %s", in.Pattern), nil
		}
		var b strings.Builder
		for _, m := range matches {
			b.WriteString(m)
			b.WriteByte('\n')
		}
		if truncated {
			fmt.Fprintf(&b, "... (%d more matches omitted; narrow the pattern)\n", omitted)
		}
		if stats.unreadable > 0 {
			fmt.Fprintf(&b, "(%d directories could not be read)\n", stats.unreadable)
		}
		details, _ := json.Marshal(globDetails{Count: len(matches), Truncated: truncated})
		return tool.Result{Content: b.String(), Truncated: truncated, Details: details}, nil
	})
}

// globMatch reports whether rel matches a pattern with the base directory
// already stripped of a trailing slash. A pattern with no `/` matches the base
// name at any depth; otherwise `permission.MatchPath` semantics apply.
func globMatch(pattern, rel string) bool {
	if pattern == "" {
		return false
	}
	if strings.Contains(pattern, "/") {
		return permission.MatchPath(pattern, rel)
	}
	return permission.MatchPath(pattern, path.Base(rel))
}

// globValid reports whether pattern is a glob the matcher can use. It mirrors
// the one invalid case the matcher rejects: an unbalanced character class.
func globValid(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '[' {
			continue
		}
		i++
		if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
			i++
		}
		if i < len(pattern) && pattern[i] == ']' {
			i++
		}
		for i < len(pattern) && pattern[i] != ']' {
			i++
		}
		if i >= len(pattern) {
			return false
		}
	}
	return true
}
