package fs

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
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
				"path": tool.StringProp("File path (relative to the working directory, or absolute)"),
				"edits": map[string]any{
					"type":        "array",
					"description": "List of exact-text replacements, matched against the original file.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"oldText": tool.StringProp("Exact text to find; must be unique in the original file"),
							"newText": tool.StringProp("Replacement text"),
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
		abs, err := tool.ResolvePath(tctx, in.Path)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}
		unlock := lockPath(abs)
		defer unlock()
		return applyEdits(abs, in.Edits), nil
	})
}

// applyEdits resolves and applies every edit to the file at abs. It writes
// through a symlink so the link survives, like write does.
func applyEdits(abs string, edits []editSpec) tool.Result {
	original, info, err := readEditableFile(abs)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	target, err := realTarget(abs)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	content, bom := strings.CutPrefix(original, string(utf8BOM))
	reps, err := resolveEdits(content, abs, edits)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	updated, changed := splice(content, reps)
	if !changed {
		return tool.Errorf("no changes: every edit left the file unchanged")
	}
	if editWriteHook != nil {
		editWriteHook()
	}
	if current, statErr := os.Stat(abs); statErr != nil || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return tool.Errorf("file changed on disk since it was read; read %s again and reapply the edit", abs)
	}
	if bom {
		updated = string(utf8BOM) + updated
	}
	if err := writeAtomic(target, []byte(updated), info.Mode().Perm()); err != nil {
		return tool.Errorf("%v", err)
	}
	return editResult(abs, original, updated, len(edits))
}

// resolveEdits matches every edit against content and returns the replacements
// sorted by position. Any edit that is empty, missing, ambiguous, or overlapping
// another fails the whole batch.
func resolveEdits(content, abs string, edits []editSpec) ([]replacement, error) {
	crlf := strings.Contains(content, "\r\n")
	reps := make([]replacement, 0, len(edits))
	for i, e := range edits {
		number := i + 1
		if e.OldText == "" {
			return nil, fmt.Errorf("edit %d: oldText must not be empty", number)
		}
		oldText, newText, ok := matchText(content, e, crlf)
		if !ok {
			return nil, fmt.Errorf("edit %d: oldText not found in %s", number, abs)
		}
		start, count := locate(content, oldText)
		if count > 1 {
			return nil, fmt.Errorf("edit %d: oldText appears %d times in %s; it must be unique", number, count, abs)
		}
		reps = append(reps, replacement{index: number, start: start, end: start + len(oldText), newText: newText})
	}
	slices.SortFunc(reps, func(a, b replacement) int { return cmp.Compare(a.start, b.start) })
	for i := 1; i < len(reps); i++ {
		if reps[i].start < reps[i-1].end {
			return nil, fmt.Errorf("edit %d overlaps edit %d", reps[i-1].index, reps[i].index)
		}
	}
	return reps, nil
}

// locate returns where sub first occurs in s and how many times it occurs.
// Overlapping matches count, so "aa" in "aaa" is ambiguous rather than unique.
func locate(s, sub string) (first, count int) {
	first = strings.Index(s, sub)
	for at := first; at >= 0; count++ {
		next := strings.Index(s[at+1:], sub)
		if next < 0 {
			return first, count + 1
		}
		at += 1 + next
	}
	return first, count
}

// readEditableFile reads a file that edit may modify: it must exist, be a
// regular text file, and be within the size cap. A named pipe or device is
// refused before it is opened, because opening one can block forever.
func readEditableFile(abs string) (string, os.FileInfo, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() {
		return "", nil, fmt.Errorf("%s is a directory", abs)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s is not a regular file", abs)
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
	if tool.LooksBinary(data) {
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

// splice applies position-sorted replacements to content. It reports whether
// anything changed, which is false only when every new text equals the text it
// replaces.
func splice(content string, reps []replacement) (string, bool) {
	var b strings.Builder
	changed := false
	prev := 0
	for _, r := range reps {
		b.WriteString(content[prev:r.start])
		b.WriteString(r.newText)
		changed = changed || content[r.start:r.end] != r.newText
		prev = r.end
	}
	b.WriteString(content[prev:])
	return b.String(), changed
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
