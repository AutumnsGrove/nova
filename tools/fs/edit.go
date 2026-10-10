package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/AutumnsGrove/nova/tool"
)

type editSpec struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// editWriteHook, when set by a test, runs after the new content is computed and
// before the on-disk change check, so a test can simulate another writer.
var editWriteHook func()

// replacement is one edit resolved to a byte range in the original content.
type replacement struct {
	index   int // 1-based edit number, for error messages
	start   int
	end     int
	newText string
}

// Edit returns the edit tool. Every edit is matched against the original file
// exactly once and non-overlapping; all edits are applied at once and written
// atomically, so a failure never leaves a half-edited file.
func Edit() tool.Tool {
	return tool.New(tool.Info{
		Name: "edit",
		Description: "Edit a file by replacing exact text. Each oldText must appear exactly once in the original file. " +
			"All edits are matched against the original and applied together. Prefer this over write for small changes.",
		Destructive: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": stringProp("File path (relative to the working directory, or absolute)"),
				"edits": map[string]any{
					"type":        "array",
					"description": "List of exact-text replacements, matched against the original file.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"oldText": stringProp("Exact text to find; must be unique in the original file"),
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
			return tool.Errorf("%v", err), nil
		}
		unlock := lockPath(abs)
		defer unlock()
		return applyEdits(abs, in.Edits), nil
	})
}

// applyEdits resolves and applies every edit to the file at abs.
func applyEdits(abs string, edits []editSpec) tool.Result {
	original, info, err := readEditableFile(abs)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	content := original
	bom := strings.HasPrefix(content, string(utf8BOM))
	if bom {
		content = strings.TrimPrefix(content, string(utf8BOM))
	}
	crlf := strings.Contains(content, "\r\n")

	reps := make([]replacement, 0, len(edits))
	for i, e := range edits {
		number := i + 1
		if e.OldText == "" {
			return tool.Errorf("edit %d: oldText must not be empty", number)
		}
		oldText, newText, ok := matchText(content, e, crlf)
		if !ok {
			return tool.Errorf("edit %d: oldText not found in %s", number, abs)
		}
		if count := strings.Count(content, oldText); count > 1 {
			return tool.Errorf("edit %d: oldText appears %d times in %s; it must be unique", number, count, abs)
		}
		start := strings.Index(content, oldText)
		reps = append(reps, replacement{index: number, start: start, end: start + len(oldText), newText: newText})
	}
	if err := checkOverlaps(reps); err != nil {
		return tool.Errorf("%v", err)
	}

	updated := content
	if newContent, changed := splice(content, reps); changed {
		updated = newContent
	} else {
		return tool.Errorf("no changes: every edit left the file unchanged")
	}
	if editWriteHook != nil {
		editWriteHook()
	}
	if current, statErr := os.Stat(abs); statErr != nil || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return tool.Errorf("file changed on disk since it was read; read %s again and reapply the edit", abs)
	}
	result := updated
	if bom {
		result = string(utf8BOM) + result
	}
	if err := writeAtomic(abs, []byte(result), info.Mode().Perm()); err != nil {
		return tool.Errorf("%v", err)
	}
	return editResult(abs, original, result, len(edits))
}

// readEditableFile reads a file that edit may modify: it must exist, be a
// regular text file, and be within the size cap.
func readEditableFile(abs string) (string, os.FileInfo, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() {
		return "", nil, fmt.Errorf("%s is a directory", abs)
	}
	if info.Size() > int64(maxFileBytes) {
		return "", nil, fmt.Errorf("%s is %d bytes, over the %d byte limit", abs, info.Size(), maxFileBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", nil, err
	}
	if len(data) > maxFileBytes {
		return "", nil, fmt.Errorf("%s is over the %d byte limit", abs, maxFileBytes)
	}
	if len(data) > binarySniff {
		data = data[:binarySniff]
	}
	if strings.IndexByte(string(data), 0) >= 0 {
		return "", nil, fmt.Errorf("%s looks like a binary file", abs)
	}
	return string(data), info, nil
}

// matchText finds the text to replace. It tries the exact oldText, then, for a
// CRLF file, a version converted from \n to \r\n. It returns the matching text
// and the replacement to use.
func matchText(content string, e editSpec, crlf bool) (oldText, newText string, ok bool) {
	if strings.Contains(content, e.OldText) {
		return e.OldText, e.NewText, true
	}
	if crlf && strings.Contains(e.OldText, "\n") && !strings.Contains(e.OldText, "\r") {
		convertedOld := strings.ReplaceAll(e.OldText, "\n", "\r\n")
		if strings.Contains(content, convertedOld) {
			return convertedOld, strings.ReplaceAll(e.NewText, "\n", "\r\n"), true
		}
	}
	return "", "", false
}

// checkOverlaps rejects edits whose matched ranges overlap.
func checkOverlaps(reps []replacement) error {
	ordered := append([]replacement(nil), reps...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].start < ordered[j].start })
	for i := 1; i < len(ordered); i++ {
		if ordered[i].start < ordered[i-1].end {
			return fmt.Errorf("edit %d overlaps edit %d", ordered[i-1].index, ordered[i].index)
		}
	}
	return nil
}

// splice applies the replacements to content. It reports whether anything
// changed, which is false only when every new text equals the text it replaces.
func splice(content string, reps []replacement) (string, bool) {
	changed := false
	for _, r := range reps {
		if content[r.start:r.end] != r.newText {
			changed = true
			break
		}
	}
	if !changed {
		return content, false
	}
	ordered := append([]replacement(nil), reps...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].start > ordered[j].start })
	var b []byte
	prev := 0
	for i := len(ordered) - 1; i >= 0; i-- {
		r := ordered[i]
		b = append(b, content[prev:r.start]...)
		b = append(b, r.newText...)
		prev = r.end
	}
	b = append(b, content[prev:]...)
	return string(b), true
}

// editResult builds the success result, including the diff unless the file is
// too large to diff.
func editResult(abs, before, after string, n int) tool.Result {
	details := map[string]any{"path": abs, "added": 0, "removed": 0}
	if len(splitLinesKeepEnds(before)) > maxDiffLines {
		details["diff_omitted"] = true
	} else if diff, added, removed, ok := unifiedDiff(abs, before, after, 3); ok {
		details["diff"] = diff
		details["added"] = added
		details["removed"] = removed
	}
	raw, _ := json.Marshal(details)
	return tool.Result{Content: fmt.Sprintf("applied %d edit(s) to %s", n, abs), Details: raw}
}
