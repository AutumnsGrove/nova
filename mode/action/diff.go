package action

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Hunk is one parsed @@ header from a unified diff.
type Hunk struct{ OldStart, OldCount, NewStart, NewCount int }

// FileDiff maps the line numbers that appear in one file's patch: removed and
// context lines on the left, added and context lines on the right. A comment is
// only valid on one of these lines.
type FileDiff struct {
	Path  string
	Left  map[int]bool
	Right map[int]bool
	Hunks []Hunk
}

// DiffFile is one pull-request file and its parsed patch.
type DiffFile struct {
	Path   string
	Patch  string
	Parsed *FileDiff
}

// NewDiffFile pairs a file's current path with its parsed patch.
func NewDiffFile(path, patch string) DiffFile {
	return DiffFile{Path: path, Patch: patch, Parsed: ParseFileDiff(path, patch)}
}

// Included reports whether this file's patch belongs in the diff sent to the
// model. A missing patch (binary or too large) and a generated path do not.
func (f DiffFile) Included() bool { return f.Patch != "" && !generatedPath(f.Path) }

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseFileDiff parses one unified patch. The path is the file's current name;
// a rename uses the new name, never the old one.
func ParseFileDiff(path, patch string) *FileDiff {
	fd := &FileDiff{Path: path, Left: map[int]bool{}, Right: map[int]bool{}}
	oldLine, newLine, valid := 0, 0, false
	for _, raw := range strings.Split(patch, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.HasPrefix(line, "@@") {
			if h, ok := parseHunk(line); ok {
				fd.Hunks = append(fd.Hunks, h)
				oldLine, newLine, valid = h.OldStart, h.NewStart, true
			}
			continue
		}
		if !valid {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			fd.Right[newLine], newLine = true, newLine+1
		case strings.HasPrefix(line, "-"):
			fd.Left[oldLine], oldLine = true, oldLine+1
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" is not a line of either side.
		case strings.HasPrefix(line, " "):
			fd.Left[oldLine], fd.Right[newLine] = true, true
			oldLine, newLine = oldLine+1, newLine+1
		}
	}
	return fd
}

// parseHunk reads a @@ header. A missing count means one line.
func parseHunk(line string) (Hunk, bool) {
	m := hunkRe.FindStringSubmatch(line)
	if m == nil {
		return Hunk{}, false
	}
	h := Hunk{OldCount: 1, NewCount: 1}
	h.OldStart, _ = strconv.Atoi(m[1])
	h.NewStart, _ = strconv.Atoi(m[3])
	if m[2] != "" {
		h.OldCount, _ = strconv.Atoi(m[2])
	}
	if m[4] != "" {
		h.NewCount, _ = strconv.Atoi(m[4])
	}
	return h, true
}

// Valid reports whether line can carry a comment on side ("LEFT" or "RIGHT").
func (f *FileDiff) Valid(line int, side string) bool {
	if strings.EqualFold(side, "LEFT") {
		return f.Left[line]
	}
	return f.Right[line]
}

// BuildDiffText concatenates the included patches up to maxBytes, stopping at a
// file boundary, and returns the paths it left out.
func BuildDiffText(files []DiffFile, maxBytes int) (string, []string) {
	var b strings.Builder
	var omitted []string
	full := false
	for _, f := range files {
		if !f.Included() {
			omitted = append(omitted, f.Path)
			continue
		}
		chunk := "### " + f.Path + "\n" + f.Patch + "\n"
		if full || (maxBytes > 0 && b.Len()+len(chunk) > maxBytes) {
			full = true
			omitted = append(omitted, f.Path)
			continue
		}
		b.WriteString(chunk)
	}
	return b.String(), omitted
}

// lockFiles are the generated lock files whose diffs are never reviewed.
var lockFiles = map[string]bool{"go.sum": true, "package-lock.json": true, "yarn.lock": true, "Cargo.lock": true, "poetry.lock": true, "composer.lock": true, "Gemfile.lock": true, "pnpm-lock.yaml": true, "bun.lockb": true}

// generatedPath reports whether a path is a lock file or lives under a vendored
// or generated directory, so its diff is never sent to the model.
func generatedPath(path string) bool {
	clean := filepath.ToSlash(path)
	if strings.HasPrefix(clean, "vendor/") || strings.HasPrefix(clean, "node_modules/") ||
		strings.Contains(clean, "/vendor/") || strings.Contains(clean, "/node_modules/") {
		return true
	}
	base := filepath.Base(clean)
	return lockFiles[base] || strings.HasSuffix(base, ".lock")
}
