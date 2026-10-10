package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture writes a small Go module to a temp directory and returns its root.
func fixture(t *testing.T, files map[string]string) string {
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

// runIn chdirs to dir and calls run, returning the exit code and both streams.
func runIn(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut, nil)
	return code, out.String(), errOut.String()
}

const goMod = "module example.com/m\n\ngo 1.26\n"

// specWithRows builds a minimal chunk spec whose matrix has the given IDs.
func specWithRows(ids ...string) string {
	var b strings.Builder
	b.WriteString("---\nchunk: C00\n---\n\n## Matrix\n\n| ID | Case |\n|---|---|\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "| %s | the %s case |\n", id, id)
	}
	return b.String()
}

// pkgTest wraps test function bodies in a package p test file.
func pkgTest(body string) string {
	return "package p\n\nimport \"testing\"\n\n" + body
}

func TestMatrixcheck_MCK01_EveryRowHasATest(t *testing.T) {
	dir := fixture(t, map[string]string{
		"go.mod":  goMod,
		"spec.md": specWithRows("AA01", "AA02"),
		"p/p_test.go": pkgTest(`
func TestA_AA01_One(t *testing.T) {}
func TestB_AA02_Two(t *testing.T) {}
`),
	})
	code, out, errOut := runIn(t, dir, "spec.md", "./p")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestMatrixcheck_MCK02_RowHasNoTest(t *testing.T) {
	dir := fixture(t, map[string]string{
		"go.mod":  goMod,
		"spec.md": specWithRows("AA01", "AA03"),
		"p/p_test.go": pkgTest(`
func TestA_AA01_One(t *testing.T) {}
`),
	})
	code, out, _ := runIn(t, dir, "spec.md", "./p")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "AA03") {
		t.Errorf("output does not name AA03:\n%s", out)
	}
}

func TestMatrixcheck_MCK03_UnknownIDCitedByTest(t *testing.T) {
	dir := fixture(t, map[string]string{
		"go.mod":  goMod,
		"spec.md": specWithRows("AA01"),
		"p/p_test.go": pkgTest(`
func TestA_AA01_One(t *testing.T) {}
func TestC_AA99_Stray(t *testing.T) {}
`),
	})
	code, out, _ := runIn(t, dir, "spec.md", "./p")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "AA99") || !strings.Contains(out, "TestC_AA99_Stray") {
		t.Errorf("output should name AA99 and the test:\n%s", out)
	}
}

func TestMatrixcheck_MCK04_OneTestCitesTwoIDs(t *testing.T) {
	dir := fixture(t, map[string]string{
		"go.mod":  goMod,
		"spec.md": specWithRows("AA01", "AA02"),
		"p/p_test.go": pkgTest(`
func TestD_AA01_AA02_Both(t *testing.T) {}
`),
	})
	code, out, errOut := runIn(t, dir, "spec.md", "./p")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestMatrixcheck_MCK05_SubtestDoesNotCount(t *testing.T) {
	dir := fixture(t, map[string]string{
		"go.mod":  goMod,
		"spec.md": specWithRows("AA01"),
		"p/p_test.go": pkgTest(`
func TestE(t *testing.T) { t.Run("AA01", func(t *testing.T) {}) }
`),
	})
	code, out, _ := runIn(t, dir, "spec.md", "./p")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "AA01") {
		t.Errorf("output does not name AA01:\n%s", out)
	}
}

func TestMatrixcheck_MCK06_IDEmbeddedInWord(t *testing.T) {
	dir := fixture(t, map[string]string{
		"go.mod":  goMod,
		"spec.md": specWithRows("FX01"),
		"p/p_test.go": pkgTest(`
func TestFX01Foo(t *testing.T) {}
`),
	})
	code, out, _ := runIn(t, dir, "spec.md", "./p")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FX01") {
		t.Errorf("output does not name FX01:\n%s", out)
	}
}

func TestMatrixcheck_MCK07_IDAsLastComponent(t *testing.T) {
	dir := fixture(t, map[string]string{
		"go.mod":  goMod,
		"spec.md": specWithRows("AA01"),
		"p/p_test.go": pkgTest(`
func TestG_AA01(t *testing.T) {}
`),
	})
	code, out, errOut := runIn(t, dir, "spec.md", "./p")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestMatrixcheck_MCK08_NoPackagesIsUsageError(t *testing.T) {
	dir := fixture(t, map[string]string{
		"spec.md": specWithRows("AA01"),
	})
	code, _, errOut := runIn(t, dir, "spec.md")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "usage") {
		t.Errorf("stderr should describe usage:\n%s", errOut)
	}
}

func TestMatrixcheck_MCK09_CompileErrorIsNotAFalsePass(t *testing.T) {
	dir := fixture(t, map[string]string{
		"go.mod":  goMod,
		"spec.md": specWithRows("AA01"),
		"p/p_test.go": pkgTest(`
func TestBroken(t *testing.T) { undefinedThing() }
`),
	})
	code, out, errOut := runIn(t, dir, "spec.md", "./p")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	combined := out + errOut
	if !strings.Contains(combined, "undefinedThing") {
		t.Errorf("output should include the compiler error:\n%s", combined)
	}
}

func TestMatrixcheck_MCK10_SpecNotFound(t *testing.T) {
	dir := fixture(t, map[string]string{"go.mod": goMod})
	code, _, errOut := runIn(t, dir, "missing.md", "./p")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "missing.md") {
		t.Errorf("stderr should name the spec:\n%s", errOut)
	}
}

func TestMatrixcheck_MCK11_BuildTagPassedThrough(t *testing.T) {
	files := map[string]string{
		"go.mod":           goMod,
		"spec.md":          specWithRows("AA01"),
		"p/tagged_test.go": "//go:build pty\n\npackage p\n\nimport \"testing\"\n\nfunc TestTagged_AA01(t *testing.T) {}\n",
		"p/plain_test.go":  pkgTest("func TestOther(t *testing.T) {}\n"),
	}
	dir := fixture(t, files)
	code, _, _ := runIn(t, dir, "spec.md", "./p")
	if code != 1 {
		t.Fatalf("without -tags: exit = %d, want 1", code)
	}
	code, out, errOut := runIn(t, dir, "-tags", "pty", "spec.md", "./p")
	if code != 0 {
		t.Fatalf("with -tags pty: exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

// TestMatrixcheck_SPC12_NoMatrixSection verifies the spec-part of SPC12: a
// spec with no matrix is a problem, reported and exited 1.
func TestMatrixcheck_SPC12_NoMatrixSection(t *testing.T) {
	dir := fixture(t, map[string]string{
		"go.mod":      goMod,
		"spec.md":     "---\nchunk: C00\n---\n\n## Goal\n\nNo matrix here.\n",
		"p/p_test.go": pkgTest("func TestA_AA01_One(t *testing.T) {}\n"),
	})
	code, out, _ := runIn(t, dir, "spec.md", "./p")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "no matrix") {
		t.Errorf("output should say the spec has no matrix:\n%s", out)
	}
}
