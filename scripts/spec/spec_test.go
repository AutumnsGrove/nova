package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSpec writes a chunk spec to a temp file and returns its path.
func writeSpec(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "C00-guards.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

const fullSpec = `---
chunk: C00
title: "Guards"
packages: [./scripts/spec, ./scripts/matrixcheck]
budgets: { ./scripts/spec: 250, ./scripts/matrixcheck: 250 }
depends: [C01]
---

# C00 - Guards

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| SPC01 | parse front matter | a spec | fields populated |
| SPC02 | no front matter | a spec | error |
`

func TestSpecLoad_SPC01_ParsesFrontMatter(t *testing.T) {
	path := writeSpec(t, fullSpec)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Chunk != "C00" {
		t.Errorf("Chunk = %q, want C00", s.Chunk)
	}
	if s.Title != "Guards" {
		t.Errorf("Title = %q, want Guards", s.Title)
	}
	if want := []string{"./scripts/spec", "./scripts/matrixcheck"}; !equalStrings(s.Packages, want) {
		t.Errorf("Packages = %v, want %v", s.Packages, want)
	}
	if s.Budgets["./scripts/spec"] != 250 || s.Budgets["./scripts/matrixcheck"] != 250 {
		t.Errorf("Budgets = %v", s.Budgets)
	}
	if want := []string{"C01"}; !equalStrings(s.Depends, want) {
		t.Errorf("Depends = %v, want %v", s.Depends, want)
	}
	if s.Path != path {
		t.Errorf("Path = %q, want %q", s.Path, path)
	}
}

func TestSpecLoad_SPC02_MissingFrontMatter(t *testing.T) {
	path := writeSpec(t, "# No front matter\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file", err)
	}
}

func TestSpecLoad_SPC03_MalformedYAML(t *testing.T) {
	path := writeSpec(t, "---\nchunk: C00\npackages: [\n---\n# body\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if !strings.Contains(err.Error(), "yaml") {
		t.Errorf("error %q does not include the YAML message", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file", err)
	}
}

func TestSpecLoad_SPC04_RowsOnlyFromMatrixSection(t *testing.T) {
	content := fullSpec + `
## Behaviour

| BAD01 | not a row | because Behaviour | ignored |
`
	s, err := Load(writeSpec(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertRowIDs(t, s, "SPC01", "SPC02")
}

func TestSpecLoad_SPC05_HeaderAndSeparatorIgnored(t *testing.T) {
	content := `---
chunk: C00
---

## Matrix

| ID | Case |
|----|------|
| XX01 | real row |
`
	s, err := Load(writeSpec(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertRowIDs(t, s, "XX01")
}

func TestSpecLoad_SPC06_TwoTablesInOneSection(t *testing.T) {
	content := strings.Join([]string{
		"---",
		"chunk: C00",
		"---",
		"# T",
		"## Matrix",
		"| ID | Case |",
		"|----|------|",
		"| XX01 | first |",
		"| XX02 | second |",
		"",
		"prose between tables",
		"",
		"| ID | Case |",
		"|----|------|",
		"| XX03 | third |",
	}, "\n")
	s, err := Load(writeSpec(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertRowIDs(t, s, "XX01", "XX02", "XX03")
	wantLines := []int{8, 9, 15}
	for i, want := range wantLines {
		if s.Rows[i].Line != want {
			t.Errorf("Rows[%d] line = %d, want %d", i, s.Rows[i].Line, want)
		}
	}
	if s.Rows[0].Case != "first" || s.Rows[2].Case != "third" {
		t.Errorf("cases = %q, %q", s.Rows[0].Case, s.Rows[2].Case)
	}
}

func TestSpecLoad_SPC07_DuplicateID(t *testing.T) {
	content := strings.Join([]string{
		"---",
		"chunk: C00",
		"---",
		"## Matrix",
		"| ID | Case |",
		"|---|---|",
		"| MSG01 | first |",
		"| MSG01 | again |",
	}, "\n")
	_, err := Load(writeSpec(t, content))
	if err == nil {
		t.Fatal("Load succeeded, want duplicate error")
	}
	for _, want := range []string{"MSG01", "7", "8"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestSpecLoad_SPC08_BadIDShape(t *testing.T) {
	content := `---
chunk: C00
---

## Matrix

| ID | Case |
|----|------|
| x1 | lowercase |
| ABCDE01 | five letters |
| AB1 | one digit |
| AB1234 | four digits |
| A01 | one letter |
| _AB01 | leading underscore |
`
	s, err := Load(writeSpec(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Rows) != 0 {
		t.Errorf("Rows = %v, want none", s.Rows)
	}
}

func TestSpecFind_SPC09_NoMatchAndTwoMatches(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := Find("C05"); err == nil || !strings.Contains(err.Error(), "no chunk spec") {
		t.Fatalf("Find with no match: err = %v, want a no-match error", err)
	}
	if err := os.MkdirAll(filepath.Join("docs", "archive", "chunks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"C05-a.md", "C05-b.md"} {
		if err := os.WriteFile(filepath.Join("docs", "archive", "chunks", name), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Find("C05")
	if err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("Find with two matches: err = %v, want a multiple-match error", err)
	}
}

func TestSpecLoad_SPC10_CRLFLineEndings(t *testing.T) {
	lf, err := Load(writeSpec(t, fullSpec))
	if err != nil {
		t.Fatalf("Load LF: %v", err)
	}
	crlf := strings.ReplaceAll(fullSpec, "\n", "\r\n")
	got, err := Load(writeSpec(t, crlf))
	if err != nil {
		t.Fatalf("Load CRLF: %v", err)
	}
	if got.Chunk != lf.Chunk || got.Title != lf.Title {
		t.Errorf("CRLF parsed as %q/%q, want %q/%q", got.Chunk, got.Title, lf.Chunk, lf.Title)
	}
	if len(got.Rows) != len(lf.Rows) {
		t.Fatalf("CRLF rows = %d, want %d", len(got.Rows), len(lf.Rows))
	}
	for i := range got.Rows {
		if got.Rows[i] != lf.Rows[i] {
			t.Errorf("row %d = %+v, want %+v", i, got.Rows[i], lf.Rows[i])
		}
	}
}

func TestSpecLoad_SPC11_MatrixMentionedInProse(t *testing.T) {
	content := strings.Join([]string{
		"---",
		"chunk: C00",
		"---",
		"## Behaviour",
		"This prose mentions ## Matrix in the middle of a line.",
		"| BAD01 | a row that belongs to Behaviour |",
		"## Matrix",
		"| XX01 | real row |",
	}, "\n")
	s, err := Load(writeSpec(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertRowIDs(t, s, "XX01")
}

func TestSpecLoad_SPC12_NoMatrixSection(t *testing.T) {
	content := "---\nchunk: C00\n---\n\n## Goal\n\nNo matrix here.\n"
	s, err := Load(writeSpec(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Rows) != 0 {
		t.Errorf("Rows = %v, want none", s.Rows)
	}
}

func TestSpecLoad_SPC13_QuotedTitleWithColon(t *testing.T) {
	content := "---\nchunk: C16\ntitle: \"TUI rendering: thinking block\"\n---\n"
	s, err := Load(writeSpec(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "TUI rendering: thinking block"; s.Title != want {
		t.Errorf("Title = %q, want %q", s.Title, want)
	}
}

func TestSpecLoad_SPC14_UnquotedTitleWithColon(t *testing.T) {
	path := writeSpec(t, "---\nchunk: C16\ntitle: TUI rendering: x\n---\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "yaml") {
		t.Errorf("error %q should name the file and the YAML message", err)
	}
}

func FuzzSpecLoad(f *testing.F) {
	f.Add([]byte(fullSpec))
	f.Add([]byte(""))
	f.Add([]byte("---\n"))
	f.Add([]byte("---\nchunk: [\n---\n"))
	f.Add([]byte("---\r\nchunk: C00\r\n---\r\n## Matrix\r\n| X01 | a |\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := parse("fuzz.md", data)
		if err == nil && s == nil {
			t.Fatal("nil spec with nil error")
		}
		if s != nil && s.Path != "fuzz.md" {
			t.Fatalf("Path = %q, want fuzz.md", s.Path)
		}
	})
}

func FuzzRowID(f *testing.F) {
	for _, seed := range []string{"", "X1", "X01", "ABC12", "ABCD123", "ABCDE01", "AB1234", "x01", "A01"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := idRe.MatchString(s), matchesIDShape(s); got != want {
			t.Fatalf("idRe.MatchString(%q) = %v, want %v", s, got, want)
		}
	})
}

// matchesIDShape is an independent reference implementation of the documented
// ID shape: two to four capital letters followed by two or three digits.
func matchesIDShape(s string) bool {
	i := 0
	for i < len(s) && s[i] >= 'A' && s[i] <= 'Z' {
		i++
	}
	letters := i
	if letters < 2 || letters > 4 {
		return false
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	return i == len(s) && digits >= 2 && digits <= 3
}

func assertRowIDs(t *testing.T, s *Spec, want ...string) {
	t.Helper()
	got := make([]string, len(s.Rows))
	for i, r := range s.Rows {
		got[i] = r.ID
	}
	if !equalStrings(got, want) {
		t.Errorf("row IDs = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
