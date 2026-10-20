//go:build !notui

// Package diff renders unified diffs for the TUI: a line-number gutter, added
// and removed backgrounds, hunk headers, wrapping, and syntax highlighting. It
// never returns an error; malformed input renders as plain text.
package diff

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/highlight"
	"github.com/AutumnsGrove/nova/tui/sanitize"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Options controls the layout of a rendered diff.
type Options struct {
	MaxLines    int  // default 60
	Wrap        bool // wrap long lines with a continuation marker
	ShowNumbers bool // two-column line-number gutter
}

// kind is the role of one unified-diff line.
type kind int

const (
	kContext kind = iota
	kAdd
	kRemove
	kHunk
	kNoNewline
	kPlain
)

// line is one parsed diff line.
type line struct {
	kind     kind
	text     string
	old, new int
}

// hunkRe matches a unified hunk header and captures the two start lines.
var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// Render formats a unified diff at width. It returns "(no changes)" for an
// empty or hunk-less diff, and plain text when the input is not a diff.
func Render(unified, path string, width int, st styles.Styles, opt Options) string {
	if opt.MaxLines == 0 {
		opt.MaxLines = 60
	}
	if width < 1 {
		width = 1
	}
	// The diff is made of file contents, so clean it once here, before any line
	// kind (hunk header, plain text, code) is drawn.
	lines := parse(sanitize.Text(unified))
	if len(lines) == 0 {
		return ansi.Truncate("(no changes)", width, "")
	}
	hasChange, hasPlain := false, false
	for _, l := range lines {
		switch l.kind {
		case kAdd, kRemove, kHunk:
			hasChange = true
		case kPlain:
			hasPlain = true
		}
	}
	if !hasChange && !hasPlain {
		return ansi.Truncate("(no changes)", width, "")
	}

	color := st.Profile > colorprofile.ASCII && st.Theme.Name != "mono"
	digits := 1
	for _, l := range lines {
		if d := len(strconv.Itoa(max(l.old, l.new))); d > digits {
			digits = d
		}
	}
	showNum := opt.ShowNumbers && width >= 20
	gutterW := 0
	if showNum {
		gutterW = digits*2 + 2
	}
	contentW := width - gutterW
	if contentW < 1 {
		contentW, showNum = width, false
	}

	var out []string
	for _, l := range lines {
		text := expandTabs(l.text, 4)
		marker := markerFor(l.kind)
		avail := contentW - ansi.StringWidth(marker) - 1 // reserve the continuation arrow
		if avail < 1 {
			avail = 1
		}
		segs := wrapSegments(text, avail, opt.Wrap)
		for si, seg := range segs {
			var body string
			switch l.kind {
			case kAdd:
				body = styleCode(st.DiffAdd, marker, seg, path, color)
			case kRemove:
				body = styleCode(st.DiffRemove, marker, seg, path, color)
			case kContext:
				body = styleCode(st.DiffContext, marker, seg, path, color)
			case kHunk:
				body = st.DiffHunk.Render(seg)
			case kNoNewline:
				body = st.DiffContext.Render(seg)
			default:
				body = seg
			}
			prefix := gutter(l.old, l.new, digits, showNum)
			if si > 0 {
				prefix = contGutter(digits, showNum)
			}
			out = append(out, ansi.Truncate(prefix+body, width, ""))
		}
	}
	if opt.MaxLines > 0 && len(out) > opt.MaxLines {
		extra := len(out) - opt.MaxLines
		out = append(out[:opt.MaxLines], ansi.Truncate(fmt.Sprintf("… %d more lines", extra), width, ""))
	}
	return strings.Join(out, "\n")
}

// markerFor returns the +, -, or space marker for a line kind.
func markerFor(k kind) string {
	switch k {
	case kAdd:
		return "+"
	case kRemove:
		return "-"
	default:
		return " "
	}
}

// styleCode highlights one segment and applies the diff style.
func styleCode(style interface{ Render(...string) string }, marker, seg, path string, color bool) string {
	body := highlight.Line(seg, path, color)
	if !color {
		return marker + body
	}
	return style.Render(marker + body)
}

// parse splits a unified diff into lines.
func parse(unified string) []line {
	raw := strings.Split(strings.ReplaceAll(unified, "\r\n", "\n"), "\n")
	if n := len(raw); n > 0 && raw[n-1] == "" {
		raw = raw[:n-1]
	}
	var out []line
	oldNo, newNo := 0, 0
	for _, l := range raw {
		switch {
		case isFileHeader(l):
			continue
		case strings.HasPrefix(l, "@@"):
			if m := hunkRe.FindStringSubmatch(l); m != nil {
				oldNo, _ = strconv.Atoi(m[1])
				newNo, _ = strconv.Atoi(m[2])
			}
			out = append(out, line{kind: kHunk, text: l})
		case strings.HasPrefix(l, `\`):
			out = append(out, line{kind: kNoNewline, text: l})
		case strings.HasPrefix(l, "+"):
			out = append(out, line{kind: kAdd, text: l[1:], new: newNo})
			newNo++
		case strings.HasPrefix(l, "-"):
			out = append(out, line{kind: kRemove, text: l[1:], old: oldNo})
			oldNo++
		case strings.HasPrefix(l, " "):
			out = append(out, line{kind: kContext, text: l[1:], old: oldNo, new: newNo})
			oldNo++
			newNo++
		case l == "":
			out = append(out, line{kind: kContext, old: oldNo, new: newNo})
			oldNo++
			newNo++
		default:
			out = append(out, line{kind: kPlain, text: l})
		}
	}
	return out
}

// isFileHeader reports whether a line is part of the diff's file header rather
// than hunk content.
func isFileHeader(l string) bool {
	for _, p := range []string{"diff ", "index ", "--- ", "+++ ", "new file", "deleted file",
		"old mode", "new mode", "similarity", "rename ", "Binary files", "GIT binary"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return l == "---" || l == "+++"
}

// wrapSegments splits a plain text line into display segments at width. When
// wrap is false, an over-long line is truncated with an ellipsis.
func wrapSegments(s string, width int, wrap bool) []string {
	if width < 1 {
		width = 1
	}
	if ansi.StringWidth(s) <= width {
		return []string{s}
	}
	if !wrap {
		return []string{ansi.Truncate(s, width-1, "") + "…"}
	}
	var segs []string
	var cur strings.Builder
	w := 0
	for _, r := range s {
		rw := ansi.StringWidth(string(r))
		if w+rw > width && cur.Len() > 0 {
			segs = append(segs, cur.String())
			cur.Reset()
			w = 0
		}
		cur.WriteRune(r)
		w += rw
	}
	segs = append(segs, cur.String())
	return segs
}

// gutter formats the two line numbers, or blanks when numbering is off.
func gutter(old, new, digits int, show bool) string {
	if !show {
		return ""
	}
	o, n := "", ""
	if old > 0 {
		o = strconv.Itoa(old)
	}
	if new > 0 {
		n = strconv.Itoa(new)
	}
	return fmt.Sprintf("%*s %*s ", digits, o, digits, n)
}

// contGutter is the right-aligned continuation marker for a wrapped line.
func contGutter(digits int, show bool) string {
	if !show {
		return "↪ "
	}
	return strings.Repeat(" ", digits*2+1) + "↪ "
}

// expandTabs replaces tabs with spaces to the next multiple of size.
func expandTabs(line string, size int) string {
	if !strings.ContainsRune(line, '\t') {
		return line
	}
	var b strings.Builder
	col := 0
	for _, r := range line {
		if r == '\t' {
			n := size - col%size
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += ansi.StringWidth(string(r))
	}
	return b.String()
}
