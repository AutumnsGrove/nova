package fs

import (
	"fmt"
	"strconv"
	"strings"
)

// diffOp is one line of an edit script: a keep, a deletion, or an insertion.
type diffOp struct {
	kind byte // ' ', '-', or '+'
	text string
}

// diffContext is how many unchanged lines surround each change in a diff, the
// default of the usual diff tools.
const diffContext = 3

// unifiedDiff returns a unified diff of before and after, with diffContext lines
// around each change. It reports how many lines were added and removed, and
// whether anything changed at all.
func unifiedDiff(path, before, after string) (string, int, int, bool) {
	if before == after {
		return "", 0, 0, false
	}
	oldLines := splitLinesKeepEnds(before)
	newLines := splitLinesKeepEnds(after)
	ops := diffOps(oldLines, newLines)

	added, removed := 0, 0
	for _, op := range ops {
		switch op.kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", path, path)
	beforeNo, afterNo := lineNumbers(ops)
	for _, h := range hunks(ops, diffContext) {
		oldCount := countOps(ops[h[0]:h[1]], " -")
		newCount := countOps(ops[h[0]:h[1]], " +")
		oldStart, newStart := beforeNo[h[0]], afterNo[h[0]]
		if oldCount == 0 {
			oldStart--
		}
		if newCount == 0 {
			newStart--
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", rangeText(oldStart, oldCount), rangeText(newStart, newCount))
		for _, op := range ops[h[0]:h[1]] {
			out.WriteByte(op.kind)
			out.WriteString(strings.TrimSuffix(op.text, "\n"))
			out.WriteByte('\n')
			if op.kind != ' ' && !strings.HasSuffix(op.text, "\n") {
				out.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	return out.String(), added, removed, true
}

// hunks groups change indices into [start,end) ranges, joining changes that are
// within 2*context keeps of each other so their context is shared.
func hunks(ops []diffOp, context int) [][2]int {
	var groups [][2]int
	last := 0
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		if len(groups) == 0 || i-last > 2*context {
			groups = append(groups, [2]int{i, i + 1})
		} else {
			groups[len(groups)-1][1] = i + 1
		}
		last = i
	}
	for i := range groups {
		groups[i][0] = max(groups[i][0]-context, 0)
		groups[i][1] = min(groups[i][1]+context, len(ops))
	}
	return groups
}

// lineNumbers returns, for each op index, the 1-based line number it starts at
// in the before and after documents.
func lineNumbers(ops []diffOp) (before, after []int) {
	before = make([]int, len(ops)+1)
	after = make([]int, len(ops)+1)
	b, a := 1, 1
	for i, op := range ops {
		before[i], after[i] = b, a
		switch op.kind {
		case ' ':
			b++
			a++
		case '-':
			b++
		case '+':
			a++
		}
	}
	before[len(ops)], after[len(ops)] = b, a
	return before, after
}

// countOps counts ops whose kind is one of the bytes in kinds.
func countOps(ops []diffOp, kinds string) int {
	n := 0
	for _, op := range ops {
		if strings.IndexByte(kinds, op.kind) >= 0 {
			n++
		}
	}
	return n
}

// rangeText renders a hunk range, omitting the count when it is one.
func rangeText(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// splitLinesKeepEnds splits s into lines, keeping each line's terminating
// newline. A final line without a newline is a distinct element, so "a" and
// "a\n" compare as different lines and the no-newline marker falls out.
func splitLinesKeepEnds(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// diffOps computes an edit script for oldLines to newLines. It strips the
// common prefix and suffix first, then runs an LCS on the middle, which is
// small for the localized edits this tool makes. A pathological middle falls
// back to replacing it wholesale, which is still a correct (if coarse) diff.
func diffOps(oldLines, newLines []string) []diffOp {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	ops := make([]diffOp, 0, len(oldLines)+len(newLines))
	for _, line := range oldLines[:prefix] {
		ops = append(ops, diffOp{' ', line})
	}
	ops = append(ops, lcsOps(oldLines[prefix:len(oldLines)-suffix], newLines[prefix:len(newLines)-suffix])...)
	for _, line := range oldLines[len(oldLines)-suffix:] {
		ops = append(ops, diffOp{' ', line})
	}
	return ops
}

// lcsOps is the edit script for the middle of two documents.
func lcsOps(a, b []string) []diffOp {
	const maxCells = 4 << 20
	if int64(len(a))*int64(len(b)) > maxCells {
		ops := make([]diffOp, 0, len(a)+len(b))
		for _, line := range a {
			ops = append(ops, diffOp{'-', line})
		}
		for _, line := range b {
			ops = append(ops, diffOp{'+', line})
		}
		return ops
	}
	dp := make([][]int32, len(a)+1)
	for i := range dp {
		dp[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	ops := make([]diffOp, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}
