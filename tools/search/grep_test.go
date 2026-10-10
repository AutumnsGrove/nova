package search

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/tool"
)

// grep runs the grep tool in dir.
func grep(t *testing.T, dir string, args map[string]any) tool.Result {
	t.Helper()
	return run(t, Grep(), &tool.Context{WorkDir: dir}, args)
}

// countMatchLines counts match lines (as opposed to context lines and notes).
func countMatchLines(content string) int {
	n := 0
	for _, line := range strings.Split(content, "\n") {
		if line == "" || line == "--" || strings.HasPrefix(line, "... (") || strings.HasPrefix(line, "(") {
			continue
		}
		if i := strings.IndexByte(line, ':'); i > 0 && !strings.Contains(line[:i], ":") {
			// A context line uses '-' before the line number instead.
			if j := strings.IndexByte(line, '-'); j < 0 || i < j {
				n++
			}
		}
	}
	return n
}

func TestGrep_GRP01_SimpleMatch(t *testing.T) {
	dir := tree(t, map[string]string{"f": "alpha\nbeta\ngamma\n"})
	res := grep(t, dir, map[string]any{"pattern": "gamma"})
	if strings.TrimSpace(res.Content) != "f:3:gamma" {
		t.Fatalf("content = %q, want f:3:gamma", res.Content)
	}
}

func TestGrep_GRP02_NoMatches(t *testing.T) {
	dir := tree(t, map[string]string{"f": "alpha\n"})
	res := grep(t, dir, map[string]any{"pattern": "zzz"})
	if res.IsError {
		t.Fatalf("result = %+v, want no-match not an error", res)
	}
	if !strings.Contains(res.Content, "no matches for zzz") {
		t.Fatalf("content = %q, want the no-match message", res.Content)
	}
}

func TestGrep_GRP03_InvalidRegex(t *testing.T) {
	dir := t.TempDir()
	res := grep(t, dir, map[string]any{"pattern": "("})
	if !res.IsError || !strings.Contains(res.Content, "invalid pattern") {
		t.Fatalf("result = %+v, want a regex parse error", res)
	}
}

func TestGrep_GRP04_EmptyPattern(t *testing.T) {
	dir := t.TempDir()
	res := grep(t, dir, map[string]any{"pattern": ""})
	if !res.IsError || !strings.Contains(res.Content, "required") {
		t.Fatalf("result = %+v, want a required-pattern error", res)
	}
}

func TestGrep_GRP05_CaseInsensitive(t *testing.T) {
	dir := tree(t, map[string]string{"f": "Foo\nfoo\n"})
	res := grep(t, dir, map[string]any{"pattern": "foo", "case_insensitive": true})
	if countMatchLines(res.Content) != 2 {
		t.Fatalf("content = %q, want both cases matched", res.Content)
	}
}

func TestGrep_GRP06_FixedStrings(t *testing.T) {
	dir := tree(t, map[string]string{"f": "a.b(\n"})
	res := grep(t, dir, map[string]any{"pattern": "a.b(", "fixed_strings": true})
	if countMatchLines(res.Content) != 1 {
		t.Fatalf("content = %q, want the literal matched", res.Content)
	}
}

func TestGrep_GRP07_RegexSpecialCharacters(t *testing.T) {
	dir := tree(t, map[string]string{"f": "axb\nayb\n"})
	res := grep(t, dir, map[string]any{"pattern": "a.b"})
	if countMatchLines(res.Content) != 2 {
		t.Fatalf("content = %q, want `.` to match any character", res.Content)
	}
}

func TestGrep_GRP08_Context(t *testing.T) {
	dir := tree(t, map[string]string{"f": "a\nMATCH\nb\nc\nd\ne\nf\nMATCH\ng\nh\n"})
	res := grep(t, dir, map[string]any{"pattern": "MATCH", "context": 2})
	if !strings.Contains(res.Content, "--") {
		t.Fatalf("content = %q, want a group separator", res.Content)
	}
	if !strings.Contains(res.Content, "f:2:MATCH") || !strings.Contains(res.Content, "f:8:MATCH") {
		t.Fatalf("content = %q, want both matches", res.Content)
	}
	if !strings.Contains(res.Content, "f-1-a") {
		t.Fatalf("content = %q, want a context line with '-'", res.Content)
	}
}

func TestGrep_GRP09_OverlappingContexts(t *testing.T) {
	dir := tree(t, map[string]string{"f": "1\n2\nMATCH\n4\nMATCH\n6\n7\n8\n"})
	res := grep(t, dir, map[string]any{"pattern": "MATCH", "context": 3})
	if strings.Contains(res.Content, "--") {
		t.Fatalf("content = %q, want overlapping contexts merged without a separator", res.Content)
	}
	for _, line := range []string{"f-1-1", "f-2-2", "f:3:MATCH"} {
		if strings.Count(res.Content, line) != 1 {
			t.Fatalf("line %q appears %d times, want 1", line, strings.Count(res.Content, line))
		}
	}
}

func TestGrep_GRP10_FirstAndLastLine(t *testing.T) {
	dir := tree(t, map[string]string{"f": "MATCH\nb\nMATCH\n"})
	res := grep(t, dir, map[string]any{"pattern": "MATCH", "context": 2})
	if !strings.Contains(res.Content, "f:1:MATCH") || !strings.Contains(res.Content, "f:3:MATCH") {
		t.Fatalf("content = %q, want both edges", res.Content)
	}
}

func TestGrep_GRP11_ContextCapped(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		if i == 50 {
			b.WriteString("MATCH\n")
		} else {
			b.WriteString("x\n")
		}
	}
	dir := tree(t, map[string]string{"f": b.String()})
	res := grep(t, dir, map[string]any{"pattern": "MATCH", "context": 99})
	if got := strings.Count(res.Content, "\n"); got != 21 {
		t.Fatalf("printed %d lines, want 21 (context capped at 10)", got)
	}
}

func TestGrep_GRP12_GlobFilter(t *testing.T) {
	dir := tree(t, map[string]string{"a.go": "needle\n", "b.txt": "needle\n"})
	res := grep(t, dir, map[string]any{"pattern": "needle", "glob": "*.go"})
	if !strings.Contains(res.Content, "a.go:") || strings.Contains(res.Content, "b.txt") {
		t.Fatalf("content = %q, want only a.go searched", res.Content)
	}
}

func TestGrep_GRP13_ExplicitFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(dir, "a.log"), "needle\n")
	res := grep(t, dir, map[string]any{"pattern": "needle", "path": "a.log"})
	if !strings.Contains(res.Content, "a.log:1:needle") {
		t.Fatalf("content = %q, want the explicit ignored file searched", res.Content)
	}
}

func TestGrep_GRP14_MaxResults(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("needle\n")
	}
	dir := tree(t, map[string]string{"f": b.String()})
	res := grep(t, dir, map[string]any{"pattern": "needle", "max_results": 5})
	if countMatchLines(res.Content) != 5 {
		t.Fatalf("content = %q, want 5 matches", res.Content)
	}
	if !res.Truncated || !strings.Contains(res.Content, "more matches omitted") {
		t.Fatalf("result = %+v, want truncation", res)
	}
}

func TestGrep_GRP15_MaxResultsCapped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 1500; i++ {
		b.WriteString("needle\n")
	}
	dir := tree(t, map[string]string{"f": b.String()})
	res := grep(t, dir, map[string]any{"pattern": "needle", "max_results": 5000})
	if countMatchLines(res.Content) != maxGrepLimit {
		t.Fatalf("matched %d, want the cap %d", countMatchLines(res.Content), maxGrepLimit)
	}
}

func TestGrep_GRP16_ContextNotMatches(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("needle\nx\n")
	}
	dir := tree(t, map[string]string{"f": b.String()})
	res := grep(t, dir, map[string]any{"pattern": "needle", "max_results": 2, "context": 1})
	if countMatchLines(res.Content) != 2 {
		t.Fatalf("content = %q, want 2 matches even with context", res.Content)
	}
	if !res.Truncated {
		t.Fatal("Truncated should be true once the match cap is reached")
	}
}

func TestGrep_GRP17_BinarySkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bin"), "needle\x00more")
	res := grep(t, dir, map[string]any{"pattern": "needle"})
	details := mustJSON(t, res.Details)
	if details["skipped"].(float64) != 1 {
		t.Fatalf("details = %v, want one skipped file", details)
	}
}

func TestGrep_GRP18_LargeFileSkipped(t *testing.T) {
	dir := t.TempDir()
	big := bytes.Repeat([]byte("a"), maxGrepFileBytes+1)
	if err := osWriteFile(filepath.Join(dir, "big"), big); err != nil {
		t.Fatalf("write: %v", err)
	}
	res := grep(t, dir, map[string]any{"pattern": "needle"})
	details := mustJSON(t, res.Details)
	if details["skipped"].(float64) != 1 {
		t.Fatalf("details = %v, want the large file skipped", details)
	}
}

func TestGrep_GRP19_VeryLongLine(t *testing.T) {
	dir := t.TempDir()
	line := strings.Repeat("a", 100000-len("needle")) + "needle"
	writeFile(t, filepath.Join(dir, "f"), line+"\n")
	res := grep(t, dir, map[string]any{"pattern": "needle"})
	if !strings.Contains(res.Content, "f:1:") {
		t.Fatalf("content = %q, want the match found", res.Content)
	}
	displayed := strings.TrimPrefix(strings.TrimSpace(res.Content), "f:1:")
	if !strings.HasSuffix(displayed, "…") {
		t.Fatalf("displayed = %q, want a cut line with an ellipsis", displayed)
	}
	if runes := []rune(displayed); len(runes) != maxGrepLineChars+1 {
		t.Fatalf("displayed %d runes, want %d", len(runes), maxGrepLineChars+1)
	}
}

func TestGrep_GRP20_LineOverOneMiB(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("a", maxGrepLineBytes+10)
	writeFile(t, filepath.Join(dir, "f"), long+"\nneedle\n")
	res := grep(t, dir, map[string]any{"pattern": "needle"})
	if countMatchLines(res.Content) != 1 {
		t.Fatalf("content = %q, want the short line matched", res.Content)
	}
	details := mustJSON(t, res.Details)
	if details["skipped"].(float64) != 1 {
		t.Fatalf("details = %v, want the over-long line skipped", details)
	}
}

func TestGrep_GRP21_CRLF(t *testing.T) {
	dir := tree(t, map[string]string{"f": "a\r\nneedle\r\nb\r\n"})
	res := grep(t, dir, map[string]any{"pattern": "needle"})
	if strings.Contains(res.Content, "\r") {
		t.Fatalf("content = %q, want the carriage return stripped", res.Content)
	}
	if !strings.Contains(res.Content, "f:2:needle") {
		t.Fatalf("content = %q, want the match", res.Content)
	}
}

func TestGrep_GRP22_InvalidUTF8(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "f"), "needle \xff\n")
	res := grep(t, dir, map[string]any{"pattern": "needle"})
	if !strings.Contains(res.Content, "\uFFFD") {
		t.Fatalf("content = %q, want invalid bytes replaced", res.Content)
	}
	if countMatchLines(res.Content) != 1 {
		t.Fatalf("content = %q, want the match still found", res.Content)
	}
}

func TestGrep_GRP23_NoTrailingNewline(t *testing.T) {
	dir := tree(t, map[string]string{"f": "a\nneedle"})
	res := grep(t, dir, map[string]any{"pattern": "needle"})
	if !strings.Contains(res.Content, "f:2:needle") {
		t.Fatalf("content = %q, want the last line searchable", res.Content)
	}
}

func TestGrep_GRP24_GitignoreAndOverride(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(dir, "a.log"), "needle\n")
	writeFile(t, filepath.Join(dir, "b.go"), "needle\n")
	res := grep(t, dir, map[string]any{"pattern": "needle"})
	if strings.Contains(res.Content, "a.log") || !strings.Contains(res.Content, "b.go") {
		t.Fatalf("content = %q, want a.log ignored", res.Content)
	}
	res = grep(t, dir, map[string]any{"pattern": "needle", "no_ignore": true})
	if !strings.Contains(res.Content, "a.log") {
		t.Fatalf("content = %q, want a.log with no_ignore", res.Content)
	}
}

func TestGrep_GRP25_HiddenFiles(t *testing.T) {
	dir := tree(t, map[string]string{".hidden": "needle\n"})
	if countMatchLines(grep(t, dir, map[string]any{"pattern": "needle"}).Content) != 0 {
		t.Fatal("hidden files should be skipped by default")
	}
	if countMatchLines(grep(t, dir, map[string]any{"pattern": "needle", "hidden": true}).Content) != 1 {
		t.Fatal("hidden files should be searched when requested")
	}
}

func TestGrep_GRP26_Cancel(t *testing.T) {
	dir := tree(t, map[string]string{"a": "needle\n", "b": "needle\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Grep().Run(ctx, []byte(`{"pattern":"needle"}`), &tool.Context{WorkDir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "context canceled") {
		t.Fatalf("result = %+v, want a cancellation error", res)
	}
}

func TestGrep_GRP27_CatastrophicRegex(t *testing.T) {
	dir := tree(t, map[string]string{"f": strings.Repeat("a", 10000) + "b\n"})
	began := time.Now()
	res := grep(t, dir, map[string]any{"pattern": "(a+)+$"})
	if elapsed := time.Since(began); elapsed > time.Second {
		t.Fatalf("grep took %v, want under 1s (RE2 is linear)", elapsed)
	}
	if !strings.Contains(res.Content, "no matches") {
		t.Fatalf("content = %q, want no match", res.Content)
	}
}

func TestGrep_GRP28_MultibytePattern(t *testing.T) {
	dir := tree(t, map[string]string{"f": "café\n"})
	res := grep(t, dir, map[string]any{"pattern": "é"})
	if countMatchLines(res.Content) != 1 {
		t.Fatalf("content = %q, want the multibyte pattern to match", res.Content)
	}
}

func TestGrep_GRP29_Details(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a"), "needle\nneedle\n")
	writeFile(t, filepath.Join(dir, "b"), "needle\n")
	writeFile(t, filepath.Join(dir, "bin"), "\x00needle")
	res := grep(t, dir, map[string]any{"pattern": "needle"})
	details := mustJSON(t, res.Details)
	if details["matches"].(float64) != 3 || details["files"].(float64) != 2 || details["skipped"].(float64) != 1 {
		t.Fatalf("details = %v, want matches 3, files 2, skipped 1", details)
	}
	if details["truncated"].(bool) {
		t.Fatalf("details = %v, want truncated false", details)
	}
}

func TestGrep_GRP30_Annotations(t *testing.T) {
	info := Grep().Info()
	if !info.ReadOnly || !info.Parallel {
		t.Fatalf("info = %+v, want ReadOnly and Parallel", info)
	}
}

func TestGrep_GRP31_LongMatchWithContext(t *testing.T) {
	dir := t.TempDir()
	long1 := strings.Repeat("x", 600) + "needle"
	long2 := strings.Repeat("y", 600)
	writeFile(t, filepath.Join(dir, "f"), long1+"\n"+long2+"\n")
	res := grep(t, dir, map[string]any{"pattern": "needle", "context": 1})
	if strings.Count(res.Content, "…") != 2 {
		t.Fatalf("content = %q, want both the match and context line cut", res.Content)
	}
}

func TestGrep_GRP32_RootsContainment(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outside := filepath.Join(dir, "outside")
	for _, p := range []string{root, outside} {
		if err := osMkdirAll(p); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	writeFile(t, filepath.Join(outside, "f"), "needle\n")
	res := run(t, Grep(), &tool.Context{WorkDir: dir, Roots: []string{root}}, map[string]any{"pattern": "needle", "path": "outside"})
	if !res.IsError {
		t.Fatalf("result = %+v, want a containment error", res)
	}
}
