package tool

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// BinarySniffBytes is how much of a file's start the file tools inspect to tell
// text from binary. Looking at a bounded window keeps classification cheap on a
// huge file, and the tools agree on one answer.
const BinarySniffBytes = 8000

// LooksBinary reports whether the start of a file holds a NUL byte within the
// first BinarySniffBytes, which text in any encoding the tools accept never does.
func LooksBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), BinarySniffBytes)], 0) >= 0
}

// maxPathBytes is the longest path a file tool accepts. It is the Linux PATH_MAX,
// so a longer string cannot name a real file.
const maxPathBytes = 4096

// ResolvePath turns a caller-supplied path into an absolute, cleaned path.
// Relative paths resolve against Context.WorkDir (or the process directory).
// When Context.Roots is set, the fully resolved path, following symlinks in the
// part that exists, must stay inside one of the roots, so a symlink or a
// symlinked parent cannot smuggle a read or write out of them. The file tools
// share this one rule so none can be weaker than the others.
func ResolvePath(tctx *Context, path string) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	if len(path) > maxPathBytes {
		return "", errors.New("path too long")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", errors.New("invalid path")
	}
	work := workDir(tctx)
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(work, abs)
	}
	abs = filepath.Clean(abs)
	if tctx != nil && len(tctx.Roots) > 0 && !underRoots(abs, tctx.Roots, work) {
		return "", errors.New("path is outside the allowed directories")
	}
	return abs, nil
}

// workDir is Context.WorkDir, or the process directory when it is empty.
func workDir(tctx *Context) string {
	if tctx != nil && tctx.WorkDir != "" {
		return filepath.Clean(tctx.WorkDir)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// underRoots reports whether the resolved path is one of the roots or below it.
// It compares with filepath.Rel rather than a string prefix so a root of "/"
// still contains everything and "/a/b" never matches the root "/a/bc".
func underRoots(abs string, roots []string, work string) bool {
	resolved := evalExisting(abs)
	for _, root := range roots {
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(work, root)
		}
		rel, err := filepath.Rel(evalExisting(filepath.Clean(root)), resolved)
		if err == nil && filepath.IsLocal(rel) {
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

// StringProp builds a JSON Schema string property for a tool's Parameters.
func StringProp(description string) map[string]any { return prop("string", description) }

// IntProp builds a JSON Schema integer property for a tool's Parameters.
func IntProp(description string) map[string]any { return prop("integer", description) }

// BoolProp builds a JSON Schema boolean property for a tool's Parameters.
func BoolProp(description string) map[string]any { return prop("boolean", description) }

func prop(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}
