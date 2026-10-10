package fs

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// applyUnified applies a unified diff produced by unifiedDiff to before. It is
// deliberately independent of the generator so the property test checks the
// diff, not the generator against itself.
func applyUnified(t *testing.T, diff, before string) string {
	t.Helper()
	if diff == "" {
		return before
	}
	old := splitLinesKeepEnds(before)
	lines := splitLinesKeepEnds(diff)
	var out strings.Builder
	oldIdx := 0
	for i := 0; i < len(lines); i++ {
		op := strings.TrimSuffix(lines[i], "\n")
		if strings.HasPrefix(op, "@@ ") {
			for oldIdx < oldStart(op) {
				out.WriteString(old[oldIdx])
				oldIdx++
			}
			for i++; i < len(lines); i++ {
				op = strings.TrimSuffix(lines[i], "\n")
				if strings.HasPrefix(op, "@@ ") {
					i--
					break
				}
				if op == "" {
					continue
				}
				switch op[0] {
				case ' ':
					if oldIdx >= len(old) {
						t.Fatalf("diff context past the end of before")
					}
					out.WriteString(old[oldIdx])
					oldIdx++
				case '-':
					oldIdx++
				case '+':
					out.WriteString(op[1:])
					if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\\") {
						i++
					} else {
						out.WriteByte('\n')
					}
				}
			}
		}
	}
	for oldIdx < len(old) {
		out.WriteString(old[oldIdx])
		oldIdx++
	}
	return out.String()
}

// oldStart parses the before-side start line of a hunk header into a 0-based
// index. A start of 0 (an insertion before line 1) clamps to 0.
func oldStart(header string) int {
	fields := strings.Fields(header)
	if len(fields) < 2 {
		return 0
	}
	rangePart := strings.TrimPrefix(fields[1], "-")
	if i := strings.IndexByte(rangePart, ','); i >= 0 {
		rangePart = rangePart[:i]
	}
	n := 0
	fmt.Sscanf(rangePart, "%d", &n)
	if n > 0 {
		n--
	}
	return n
}

// TestDiff_DIF01_NoChange checks equal input produces no diff.
func TestDiff_DIF01_NoChange(t *testing.T) {
	diff, added, removed, ok := unifiedDiff("f", "a\n", "a\n", 3)
	if ok || diff != "" || added != 0 || removed != 0 {
		t.Fatalf("diff=%q added=%d removed=%d ok=%v", diff, added, removed, ok)
	}
}

// TestDiff_DIF02_OneLine checks one changed line.
func TestDiff_DIF02_OneLine(t *testing.T) {
	diff, added, removed, ok := unifiedDiff("f", "a\nb\nc\n", "a\nX\nc\n", 3)
	if !ok || added != 1 || removed != 1 {
		t.Fatalf("added=%d removed=%d ok=%v", added, removed, ok)
	}
	if !strings.Contains(diff, "-b") || !strings.Contains(diff, "+X") {
		t.Fatalf("diff = %q", diff)
	}
}

// TestDiff_DIF03_InsertDelete checks an insertion and a deletion.
func TestDiff_DIF03_InsertDelete(t *testing.T) {
	before := "a\nb\nc\nd\n"
	after := "a\nc\nX\nd\n"
	diff, added, removed, _ := unifiedDiff("f", before, after, 3)
	if added != 1 || removed != 1 {
		t.Fatalf("added=%d removed=%d, want 1 and 1", added, removed)
	}
	if got := applyUnified(t, diff, before); got != after {
		t.Fatalf("applied = %q, want %q", got, after)
	}
}

// TestDiff_DIF04_Context checks three lines of context on either side.
func TestDiff_DIF04_Context(t *testing.T) {
	before := "1\n2\n3\n4\n5\n6\n7\n8\n9\n"
	after := "1\n2\n3\n4\nX\n6\n7\n8\n9\n"
	diff, _, _, _ := unifiedDiff("f", before, after, 3)
	context := 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, " ") {
			context++
		}
	}
	if context != 6 {
		t.Fatalf("context lines = %d, want 6\ndiff:\n%s", context, diff)
	}
}

// TestDiff_DIF05_TwoHunks checks two far-apart changes produce two hunks.
func TestDiff_DIF05_TwoHunks(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\n"
	after := "A\nb\nc\nd\ne\nf\ng\nh\ni\nJ\nk\n"
	diff, _, _, _ := unifiedDiff("f", before, after, 3)
	if got := strings.Count(diff, "@@ -"); got != 2 {
		t.Fatalf("hunks = %d, want 2\ndiff:\n%s", got, diff)
	}
	if got := applyUnified(t, diff, before); got != after {
		t.Fatalf("applied = %q, want %q", got, after)
	}
}

// TestDiff_DIF06_NoNewline checks the no-newline marker.
func TestDiff_DIF06_NoNewline(t *testing.T) {
	diff, _, _, _ := unifiedDiff("f", "a\nb", "a\nB", 3)
	if !strings.Contains(diff, "\\ No newline at end of file") {
		t.Fatalf("diff = %q, want the no-newline marker", diff)
	}
}

// TestDiff_DIF07_ApplyReproduces checks applying the diff reproduces after.
func TestDiff_DIF07_ApplyReproduces(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 300; i++ {
		before := randomText(rng)
		after := mutate(rng, before)
		diff, _, _, _ := unifiedDiff("f", before, after, 3)
		got := applyUnified(t, diff, before)
		if got != after {
			t.Fatalf("case %d: before=%q after=%q applied=%q\ndiff:\n%s", i, before, after, got, diff)
		}
	}
}

// randomText builds a small random document.
func randomText(rng *rand.Rand) string {
	var b strings.Builder
	for i := 0; i < rng.Intn(12); i++ {
		fmt.Fprintf(&b, "line %d\n", rng.Intn(5))
	}
	return b.String()
}

// mutate changes a random part of text.
func mutate(rng *rand.Rand, text string) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	switch rng.Intn(3) {
	case 0:
		if len(lines) > 0 {
			lines[rng.Intn(len(lines))] = "changed"
		}
	case 1:
		lines = append(lines, "added")
	case 2:
		if len(lines) > 0 {
			i := rng.Intn(len(lines))
			lines = append(lines[:i], lines[i+1:]...)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	out := strings.Join(lines, "\n")
	if strings.HasSuffix(text, "\n") {
		out += "\n"
	}
	return out
}

// FuzzUnifiedDiff checks the diff never panics and applying it reproduces after.
func FuzzUnifiedDiff(f *testing.F) {
	f.Add("a\nb\n", "a\nc\n")
	f.Add("", "x")
	f.Add("x", "")
	f.Fuzz(func(t *testing.T, before, after string) {
		if len(before) > 4096 || len(after) > 4096 {
			return
		}
		diff, _, _, _ := unifiedDiff("f", before, after, 3)
		got := applyUnified(t, diff, before)
		if got != after {
			t.Fatalf("before=%q after=%q applied=%q\ndiff:\n%s", before, after, got, diff)
		}
	})
}
