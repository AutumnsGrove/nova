// Package search provides the read-only search tools: glob and grep.
//
// Both walk a directory tree with the same rules (deterministic order,
// .gitignore, hidden files, symlink safety) and cap their output so one call can
// never flood the model's context.
package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AutumnsGrove/nova/tool"
)

const pathMaxBytes = 4096

// resolve turns a caller-supplied path into an absolute, cleaned path. Relative
// paths resolve against Context.WorkDir (or the process directory). When
// Context.Roots is set, the fully resolved path, following symlinks in the part
// that exists, must stay inside one of the roots. The containment rule matches
// the filesystem tools, so a search cannot read outside a configured root.
func resolve(tctx *tool.Context, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if len(path) > pathMaxBytes {
		return "", fmt.Errorf("path too long")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("invalid path")
	}
	work := workDir(tctx)
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(work, abs)
	}
	abs = filepath.Clean(abs)
	if tctx != nil && len(tctx.Roots) > 0 && !underRoots(abs, tctx.Roots, work) {
		return "", fmt.Errorf("path is outside the allowed directories")
	}
	return abs, nil
}

// workDir is Context.WorkDir, or the process directory when it is empty.
func workDir(tctx *tool.Context) string {
	if tctx != nil && tctx.WorkDir != "" {
		return filepath.Clean(tctx.WorkDir)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// underRoots reports whether the resolved path is one of the roots or below it.
func underRoots(abs string, roots []string, work string) bool {
	resolved := evalExisting(abs)
	for _, root := range roots {
		if root == "" {
			continue
		}
		rootAbs := root
		if !filepath.IsAbs(rootAbs) {
			rootAbs = filepath.Join(work, rootAbs)
		}
		rootAbs = evalExisting(filepath.Clean(rootAbs))
		if resolved == rootAbs || strings.HasPrefix(resolved, rootAbs+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// evalExisting resolves the deepest existing ancestor of path, follows its
// symlinks, and re-appends the part that does not exist yet, so a path created
// under a symlinked parent is still checked against the real directory.
func evalExisting(path string) string {
	existing := path
	var tail []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		tail = append([]string{filepath.Base(existing)}, tail...)
		existing = parent
	}
	if resolved, err := filepath.EvalSymlinks(existing); err == nil {
		existing = resolved
	}
	return filepath.Join(append([]string{existing}, tail...)...)
}

// stringProp is a small helper for building JSON Schema property maps.
func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// boolProp is a small helper for building JSON Schema property maps.
func boolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

// intProp is a small helper for building JSON Schema property maps.
func intProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}
