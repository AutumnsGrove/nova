// Package fs provides the core filesystem tools: read, write, edit, and list.
package fs

import (
	"fmt"
	"path/filepath"

	"github.com/AutumnsGrove/nova/tool"
)

// resolve turns a caller-supplied path into an absolute path. Relative paths
// resolve against the tool context's working directory. Absolute paths are
// accepted as-is.
func resolve(tctx *tool.Context, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	workDir := ""
	if tctx != nil {
		workDir = tctx.WorkDir
	}
	if workDir == "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		return filepath.Clean(abs), nil
	}
	return filepath.Clean(filepath.Join(workDir, path)), nil
}

// stringProp is a small helper for building JSON Schema property maps.
func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// intProp is a small helper for building JSON Schema property maps.
func intProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}
