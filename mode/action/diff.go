package action

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Hunk is one parsed @@ header from a unified diff.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
}

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
func (f DiffFile) Included() bool {
	return f.Patch != "" && !generatedPath(f.Path)
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseFileDiff parses one unified patch. The path is the file's current name;
// a rename uses the new name, never the old one.
func ParseFileDiff(path, patch string) *FileDiff {
	fd := &FileDiff{Path: path, Left: map[int]bool{}, Right: map[int]bool{}}
	var oldLine, newLine int
	valid := false
	for _, raw := range strings.Split(patch, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.HasPrefix(line, "@@") {
			var h Hunk
			var ok bool
			if h, ok = parseHunk(line); ok {
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
			fd.Right[newLine] = true
			newLine++
		case strings.HasPrefix(line, "-"):
			fd.Left[oldLine] = true
			oldLine++
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" is not a line of either side.
		case strings.HasPrefix(line, " "):
			// A context line. An empty line is ignored, not a context line.
			fd.Left[oldLine] = true
			fd.Right[newLine] = true
			oldLine++
			newLine++
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
	if m[2] != "" {
		h.OldCount, _ = strconv.Atoi(m[2])
	}
	h.NewStart, _ = strconv.Atoi(m[3])
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

// Bounds returns the highest line number the patch maps on each side.
func (f *FileDiff) Bounds() (left, right int) {
	for _, h := range f.Hunks {
		if end := h.OldStart + h.OldCount - 1; end > left {
			left = end
		}
		if end := h.NewStart + h.NewCount - 1; end > right {
			right = end
		}
	}
	return left, right
}

// BuildDiffText concatenates the included patches up to maxBytes, stopping at a
// file boundary, and returns the paths it left out (binary, generated, or cut
// for size).
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

// generatedPath reports whether a path is a lock file or lives under a vendored
// or generated directory, so its diff is never sent to the model.
func generatedPath(path string) bool {
	clean := filepath.ToSlash(path)
	for _, dir := range []string{"vendor/", "node_modules/"} {
		if strings.HasPrefix(clean, dir) || strings.Contains(clean, "/"+dir) {
			return true
		}
	}
	base := filepath.Base(clean)
	if strings.HasSuffix(base, ".lock") {
		return true
	}
	switch base {
	case "go.sum", "package-lock.json", "yarn.lock", "Cargo.lock", "poetry.lock",
		"composer.lock", "Gemfile.lock", "pnpm-lock.yaml", "bun.lockb":
		return true
	}
	return false
}
