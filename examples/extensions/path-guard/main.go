// Command path-guard blocks write and edit calls that leave the working directory.
package main

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/AutumnsGrove/nova/ext/sdk"
)

func main() {
	sdk.Run(func(h *sdk.Host) error {
		h.On("tool_call", func(e *sdk.Hook) (*sdk.HookDecision, error) {
			var event struct {
				Tool      string          `json:"tool"`
				Arguments json.RawMessage `json:"arguments"`
			}
			// An event the hook cannot read is an error, which the SDK answers
			// as "no opinion"; returning nil, nil would hide it.
			if err := e.Decode(&event); err != nil {
				return nil, err
			}
			if event.Tool != "write" && event.Tool != "edit" {
				return nil, nil
			}
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(event.Arguments, &args); err != nil {
				return nil, err
			}
			if outside(args.Path) {
				return &sdk.HookDecision{
					Block:  true,
					Reason: "path-guard: refusing to write outside the working directory",
				}, nil
			}
			return nil, nil
		})
		return nil
	})
}

// outside reports whether path is absolute or climbs out of the directory.
func outside(path string) bool {
	if path == "" {
		return false
	}
	if filepath.IsAbs(path) {
		return true
	}
	clean := filepath.Clean(path)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
