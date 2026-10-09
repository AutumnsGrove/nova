package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/AutumnsGrove/nova/tool"
)

type editSpec struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// Edit returns the edit tool. Each edit replaces an exact, unique string. Edits
// are applied in order, so an edit whose oldText no longer exists after an
// earlier edit fails loudly rather than corrupting the file.
func Edit() tool.Tool {
	return tool.New(tool.Info{
		Name: "edit",
		Description: "Edit a file by replacing exact text. Each oldText must appear exactly once in the file at the time it is applied. " +
			"Prefer this over write for small changes: it preserves surrounding content.",
		Destructive: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": stringProp("File path (relative to the working directory, or absolute)"),
				"edits": map[string]any{
					"type":        "array",
					"description": "Ordered list of exact-text replacements.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"oldText": stringProp("Exact text to find; must be unique in the file"),
							"newText": stringProp("Replacement text"),
						},
						"required": []string{"oldText", "newText"},
					},
				},
			},
			"required": []string{"path", "edits"},
		},
	}, func(ctx context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
		var in struct {
			Path  string     `json:"path"`
			Edits []editSpec `json:"edits"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		if len(in.Edits) == 0 {
			return tool.Errorf("edits must contain at least one replacement"), nil
		}
		abs, err := resolve(tctx, in.Path)
		if err != nil {
			return tool.Result{}, err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		content := string(data)

		for i, e := range in.Edits {
			if e.OldText == "" {
				return tool.Errorf("edit %d: oldText must not be empty", i+1), nil
			}
			count := strings.Count(content, e.OldText)
			if count == 0 {
				return tool.Errorf("edit %d: oldText not found in %s", i+1, abs), nil
			}
			if count > 1 {
				return tool.Errorf("edit %d: oldText appears %d times in %s; it must be unique", i+1, count, abs), nil
			}
			content = strings.Replace(content, e.OldText, e.NewText, 1)
		}

		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return tool.Errorf("%v", err), nil
		}
		return tool.Text("applied %d edit(s) to %s", len(in.Edits), abs), nil
	})
}
