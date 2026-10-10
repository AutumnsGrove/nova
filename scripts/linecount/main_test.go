package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree writes files (with directories as needed) under a temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// specText builds a front matter with the given packages and budgets.
func specText(pkgs []string, budgets map[string]int) string {
	var b strings.Builder
	b.WriteString("---\nchunk: C00\npackages: [")
	b.WriteString(strings.Join(pkgs, ", "))
	b.WriteString("]\n")
	if budgets != nil {
		b.WriteString("budgets:\n")
		for _, p := range pkgs {
			if n, ok := budgets[p]; ok {
				fmt.Fprintf(&b, "  %s: %d\n", p, n)
			}
		}
	}
	b.WriteString("---\n")
	return b.String()
}

// codeFile returns n lines that each contain a Go token.
func codeFile(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "var n%d = %d\n", i, i)
	}
	return b.String()
}

func runIn(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut, nil)
	return code, out.String(), errOut.String()
}

func TestLinecount_LCT01_WithinBudget(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"spec.md": specText([]string{"./p"}, map[string]int{"./p": 200}),
		"p/a.go":  codeFile(100),
	})
	code, out, errOut := runIn(t, dir, "spec.md")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestLinecount_LCT02_OverBudget(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"spec.md": specText([]string{"./p"}, map[string]int{"./p": 200}),
		"p/a.go":  codeFile(201),
	})
	code, out, _ := runIn(t, dir, "spec.md")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "201") || !strings.Contains(out, "200") {
		t.Errorf("output should show count and budget:\n%s", out)
	}
}

func TestLinecount_LCT03_BlankAndCommentsExcluded(t *testing.T) {
	var b strings.Builder
	b.WriteString(codeFile(50))
	for i := 0; i < 50; i++ {
		b.WriteString("\n")
	}
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "// comment %d\n", i)
	}
	dir := writeTree(t, map[string]string{
		"spec.md": specText([]string{"./p"}, map[string]int{"./p": 50}),
		"p/a.go":  b.String(),
	})
	code, out, errOut := runIn(t, dir, "spec.md")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (blanks/comments must not count)\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestLinecount_LCT04_TestFilesExcluded(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"spec.md":     specText([]string{"./p"}, map[string]int{"./p": 10}),
		"p/a.go":      codeFile(5),
		"p/a_test.go": codeFile(500),
	})
	code, out, errOut := runIn(t, dir, "spec.md")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (test files must not count)\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestLinecount_LCT05_PackageWithoutBudget(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"spec.md": specText([]string{"./a", "./b"}, map[string]int{"./a": 100}),
		"a/a.go":  codeFile(1),
		"b/b.go":  codeFile(1),
	})
	code, out, errOut := runIn(t, dir, "spec.md")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(out+errOut, "./b") {
		t.Errorf("output should name ./b:\n%s%s", out, errOut)
	}
}

func TestLinecount_LCT06_BlockCommentsExcluded(t *testing.T) {
	content := "var a = 1\n/* block\nstill comment\nend */\n"
	dir := writeTree(t, map[string]string{
		"spec.md": specText([]string{"./p"}, map[string]int{"./p": 1}),
		"p/a.go":  content,
	})
	code, out, errOut := runIn(t, dir, "spec.md")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (block comment lines must not count)\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestLinecount_LCT07_TrailingCommentCountsAsCode(t *testing.T) {
	content := "var a = 1 // trailing comment\n// comment only\n"
	dir := writeTree(t, map[string]string{
		"spec.md": specText([]string{"./p"}, map[string]int{"./p": 1}),
		"p/a.go":  content,
	})
	code, out, errOut := runIn(t, dir, "spec.md")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (one code line, one comment line)\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}
