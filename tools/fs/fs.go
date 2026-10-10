// Package fs provides the core filesystem tools: read, write, edit, and list.
package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/AutumnsGrove/nova/tool"
)

// Limits. They are variables so tests can lower them without re-reading a huge
// file, and named here so every tool agrees on the caps.
var (
	maxReadLines   = 2000     // lines read returns by default and at most
	maxReadBytes   = 51200    // bytes of read output before it stops
	maxLineChars   = 2000     // runes of one displayed line before truncation
	maxFileBytes   = 10 << 20 // write and edit refuse content larger than this
	maxListEntries = 500      // list rows before it stops
	binarySniff    = 8000     // bytes sniffed for a NUL when classifying a file
	maxDiffLines   = 5000     // files larger than this get no diff in Details
	maxPathBytes   = 4096     // longest accepted path
)

// resolve turns a caller-supplied path into an absolute, cleaned path. Relative
// paths resolve against Context.WorkDir (or the process directory). When
// Context.Roots is set, the fully resolved path, following symlinks in the part
// that exists, must stay inside one of the roots.
func resolve(tctx *tool.Context, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if len(path) > maxPathBytes {
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
		rootAbs := root
		if rootAbs == "" {
			continue
		}
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
// symlinks, and re-appends the part that does not exist yet. A path that does
// not exist is therefore checked against the real directory it would be
// created in, so a symlinked parent cannot smuggle a write outside a root.
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

// pathLocks serialises write and edit on the same path without serialising
// unrelated paths. The map is only ever appended to; a lock is tiny and the
// process holds at most one per path it has touched.
var pathLocks sync.Map

// lockPath locks the mutex for path and returns its unlock function.
func lockPath(path string) func() {
	value, _ := pathLocks.LoadOrStore(path, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// stringProp is a small helper for building JSON Schema property maps.
func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// intProp is a small helper for building JSON Schema property maps.
func intProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}
