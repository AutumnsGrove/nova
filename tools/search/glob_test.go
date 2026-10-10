package search

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/tool"
)

func TestGlob_GLT01_BaseNameAnyDepth(t *testing.T) {
	dir := tree(t, map[string]string{"a.go": "x", "x/b.go": "x", "c.txt": "x"})
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "*.go"})
	if !strings.Contains(res.Content, "a.go") || !strings.Contains(res.Content, "x/b.go") {
		t.Fatalf("content = %q, want both .go files", res.Content)
	}
	if strings.Contains(res.Content, "c.txt") {
		t.Fatalf("content = %q, want no .txt file", res.Content)
	}
}

func TestGlob_GLT02_DoubleStar(t *testing.T) {
	dir := tree(t, map[string]string{"a.go": "x", "x/b.go": "x", "c.txt": "x"})
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "**/*.go"})
	if !strings.Contains(res.Content, "a.go") || !strings.Contains(res.Content, "x/b.go") {
		t.Fatalf("content = %q, want all .go files", res.Content)
	}
}

func TestGlob_GLT03_DoubleStarUnderDir(t *testing.T) {
	dir := tree(t, map[string]string{"src/a.go": "x", "src/x/b.go": "x", "other/c.go": "x"})
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "src/**/*.go"})
	if !strings.Contains(res.Content, "src/a.go") || !strings.Contains(res.Content, "src/x/b.go") {
		t.Fatalf("content = %q, want src files", res.Content)
	}
	if strings.Contains(res.Content, "other/c.go") {
		t.Fatalf("content = %q, want only src files", res.Content)
	}
}

func TestGlob_GLT04_AnchoredStar(t *testing.T) {
	dir := tree(t, map[string]string{"src/a/b.go": "x", "src/c.go": "x"})
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "src/*.go"})
	if !strings.Contains(res.Content, "src/c.go") {
		t.Fatalf("content = %q, want src/c.go", res.Content)
	}
	if strings.Contains(res.Content, "src/a/b.go") {
		t.Fatalf("content = %q, want src/a/b.go excluded", res.Content)
	}
}

func TestGlob_GLT05_DirectoriesOnly(t *testing.T) {
	dir := tree(t, map[string]string{"a/b/x.txt": "x", "file.txt": "x"})
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "**/"})
	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	if strings.Join(lines, ",") != "a,a/b" {
		t.Fatalf("content = %q, want the directories only", res.Content)
	}
}

func TestGlob_GLT06_NoMatches(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "x"})
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "*.go"})
	if res.IsError {
		t.Fatalf("result = %+v, want no-match not an error", res)
	}
	if !strings.Contains(res.Content, "no files match *.go") {
		t.Fatalf("content = %q, want the no-match message", res.Content)
	}
}

func TestGlob_GLT07_InvalidPattern(t *testing.T) {
	dir := t.TempDir()
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "["})
	if !res.IsError || !strings.Contains(res.Content, "invalid pattern") {
		t.Fatalf("result = %+v, want an invalid pattern error", res)
	}
}

func TestGlob_GLT08_SortedRelative(t *testing.T) {
	dir := tree(t, map[string]string{"x/c.go": "x", "b.go": "x", "a.go": "x"})
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "*.go"})
	if got := strings.TrimSpace(res.Content); got != "a.go\nb.go\nx/c.go" {
		t.Fatalf("content = %q, want sorted relative paths", got)
	}
}

func TestGlob_GLT09_Truncated(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 1500; i++ {
		files[fmt.Sprintf("f%04d.txt", i)] = "x"
	}
	dir := tree(t, files)
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "*.txt"})
	if !res.Truncated {
		t.Fatal("Truncated should be true")
	}
	if !strings.Contains(res.Content, "500 more matches omitted") {
		t.Fatalf("content should name the omitted count: %q", res.Content)
	}
	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	returned := 0
	for _, line := range lines {
		if !strings.HasPrefix(line, "... (") && !strings.HasPrefix(line, "(") {
			returned++
		}
	}
	if returned != maxGlobResults {
		t.Fatalf("returned %d matches, want %d", returned, maxGlobResults)
	}
}

func TestGlob_GLT10_PathSubdirectory(t *testing.T) {
	dir := tree(t, map[string]string{"sub/a.go": "x", "b.go": "x"})
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "*.go", "path": "sub"})
	if strings.TrimSpace(res.Content) != "a.go" {
		t.Fatalf("content = %q, want a.go relative to sub", res.Content)
	}
}

func TestGlob_GLT11_PathMissingOrFile(t *testing.T) {
	dir := tree(t, map[string]string{"a.txt": "x"})
	if res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "*", "path": "missing"}); !res.IsError {
		t.Fatalf("missing path result = %+v, want an error", res)
	}
	if res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "*", "path": "a.txt"}); !res.IsError {
		t.Fatalf("file path result = %+v, want an error", res)
	}
}

func TestGlob_GLT12_GitignoreHonoured(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(dir, "a.log"), "x")
	writeFile(t, filepath.Join(dir, "b.go"), "x")
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "*"})
	if strings.Contains(res.Content, "a.log") || !strings.Contains(res.Content, "b.go") {
		t.Fatalf("content = %q, want the ignored file absent", res.Content)
	}
}

func TestGlob_GLT13_Details(t *testing.T) {
	dir := tree(t, map[string]string{"a.go": "x", "b.go": "x"})
	res := run(t, Glob(), &tool.Context{WorkDir: dir}, map[string]any{"pattern": "*.go"})
	details := mustJSON(t, res.Details)
	if details["count"].(float64) != 2 || details["truncated"].(bool) {
		t.Fatalf("details = %v, want count 2 and truncated false", details)
	}
}

func TestGlob_GLT14_Annotations(t *testing.T) {
	info := Glob().Info()
	if !info.ReadOnly || !info.Parallel {
		t.Fatalf("info = %+v, want ReadOnly and Parallel", info)
	}
}

func FuzzGlobPattern(f *testing.F) {
	f.Add("**/*.go", "src/a/b.go")
	f.Add("*.go", "x/b.go")
	f.Add("[ab].txt", "a.txt")
	f.Add("a/**/b", "a/x/y/b")
	f.Add("src/*.go", "src/a/b.go")
	f.Fuzz(func(t *testing.T, pattern, target string) {
		if len(pattern) > 1024 || len(target) > 1024 {
			t.Skip()
		}
		globValid(pattern)
		globMatch(strings.TrimSuffix(pattern, "/"), target)
	})
}
