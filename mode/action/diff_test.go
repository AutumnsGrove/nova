package action

import (
	"strings"
	"testing"
)

const basicPatch = `@@ -1,4 +1,5 @@
 context
-removed
+added
 context2
`

func TestAction_PDI01_AddedRemovedContext(t *testing.T) {
	fd := ParseFileDiff("a.go", basicPatch)
	if !fd.Left[1] || !fd.Left[2] || !fd.Left[3] {
		t.Fatalf("left = %v, want 1,2,3", fd.Left)
	}
	if !fd.Right[1] || !fd.Right[2] || !fd.Right[3] {
		t.Fatalf("right = %v, want 1,2,3", fd.Right)
	}
	if fd.Right[4] {
		t.Fatalf("right maps an extra line: %v", fd.Right)
	}
}

func TestAction_PDI02_MultipleHunks(t *testing.T) {
	patch := "@@ -1,2 +1,2 @@\n a\n-b\n+B\n@@ -10,2 +10,3 @@\n c\n+d\n e\n"
	fd := ParseFileDiff("a.go", patch)
	if len(fd.Hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(fd.Hunks))
	}
	if !fd.Left[2] || !fd.Right[2] {
		t.Fatalf("first hunk: left=%v right=%v", fd.Left, fd.Right)
	}
	if !fd.Left[10] || !fd.Left[11] {
		t.Fatalf("second hunk left = %v", fd.Left)
	}
	if !fd.Right[10] || !fd.Right[11] || !fd.Right[12] {
		t.Fatalf("second hunk right = %v", fd.Right)
	}
}

func TestAction_PDI03_RenamedFileUsesNewPath(t *testing.T) {
	f := NewDiffFile("pkg/new.go", basicPatch)
	if f.Path != "pkg/new.go" || f.Parsed.Path != "pkg/new.go" {
		t.Fatalf("rename path = %q / %q, want pkg/new.go", f.Path, f.Parsed.Path)
	}
}

func TestAction_PDI04_BinaryAndMissingPatch(t *testing.T) {
	files := []DiffFile{
		NewDiffFile("a.png", ""),
		NewDiffFile("vendor/dep.go", basicPatch),
		NewDiffFile("package-lock.json", basicPatch),
		NewDiffFile("real.go", basicPatch),
	}
	text, omitted := BuildDiffText(files, 1<<20)
	if !strings.Contains(text, "### real.go") {
		t.Fatalf("real.go was not included: %q", text)
	}
	for _, path := range []string{"a.png", "vendor/dep.go", "package-lock.json"} {
		if !contains(omitted, path) {
			t.Fatalf("omitted = %v, want %s", omitted, path)
		}
	}
}

func TestAction_PDI05_NoNewlineMarker(t *testing.T) {
	patch := "@@ -1 +1 @@\n-old\n+new\n\\ No newline at end of file\n"
	fd := ParseFileDiff("a.go", patch)
	if !fd.Left[1] || !fd.Right[1] {
		t.Fatalf("left=%v right=%v", fd.Left, fd.Right)
	}
	if fd.Left[2] || fd.Right[2] {
		t.Fatalf("the marker shifted the line numbers: left=%v right=%v", fd.Left, fd.Right)
	}
}

func TestAction_PDI06_HugeDiffTruncatedAtFileBoundary(t *testing.T) {
	big := "@@ -1 +1 @@\n" + "+" + strings.Repeat("x", 2000) + "\n"
	files := []DiffFile{
		NewDiffFile("one.go", big),
		NewDiffFile("two.go", big),
		NewDiffFile("three.go", big),
	}
	text, omitted := BuildDiffText(files, 2500)
	if !strings.Contains(text, "### one.go") {
		t.Fatalf("one.go missing: %q", text)
	}
	if strings.Contains(text, "### two.go") || strings.Contains(text, "### three.go") {
		t.Fatalf("truncation crossed a file boundary: %q", text)
	}
	if !contains(omitted, "two.go") || !contains(omitted, "three.go") {
		t.Fatalf("omitted = %v", omitted)
	}
}

func TestAction_PDI07_LineValidation(t *testing.T) {
	fd := ParseFileDiff("a.go", basicPatch)
	if !fd.Valid(2, "RIGHT") {
		t.Fatal("an added line on the right should be valid")
	}
	if !fd.Valid(1, "RIGHT") {
		t.Fatal("a context line on the right should be valid")
	}
	if !fd.Valid(2, "LEFT") {
		t.Fatal("a removed line on the left should be valid")
	}
	if fd.Valid(99, "RIGHT") || fd.Valid(99, "LEFT") {
		t.Fatal("a line outside every hunk should be invalid")
	}
}

func TestAction_PDI08_CRLF(t *testing.T) {
	patch := strings.ReplaceAll(basicPatch, "\n", "\r\n")
	fd := ParseFileDiff("a.go", patch)
	if !fd.Right[1] || !fd.Right[2] || !fd.Left[2] {
		t.Fatalf("CRLF patch: left=%v right=%v", fd.Left, fd.Right)
	}
}

func TestAction_PDI09_HunkHeaderEdges(t *testing.T) {
	fd := ParseFileDiff("a.go", "@@ -0,0 +1 @@\n+first\n")
	if !fd.Right[1] || len(fd.Left) != 0 {
		t.Fatalf("zero-start hunk: left=%v right=%v", fd.Left, fd.Right)
	}
	fd = ParseFileDiff("a.go", "@@ -1 +1 @@\n-only\n+only\n")
	if len(fd.Hunks) != 1 || fd.Hunks[0].OldCount != 1 || fd.Hunks[0].NewCount != 1 {
		t.Fatalf("countless header = %+v", fd.Hunks)
	}
}

func FuzzParseDiff(f *testing.F) {
	f.Add("")
	f.Add(basicPatch)
	f.Add("@@ -1,2 +1,2 @@\n a\n-b\n+B\n")
	f.Add("@@ -0,0 +1 @@\n+first\n")
	f.Add("@@ garbage\n+orphan\n")
	f.Add("@@ -1,1 +1,1 @@\r\n a\r\n")
	f.Fuzz(func(t *testing.T, patch string) {
		fd := ParseFileDiff("f", patch)
		left, right := fd.Bounds()
		for line := range fd.Left {
			if line < 0 || line > left {
				t.Fatalf("left line %d outside bounds 0..%d", line, left)
			}
		}
		for line := range fd.Right {
			if line < 0 || line > right {
				t.Fatalf("right line %d outside bounds 0..%d", line, right)
			}
		}
	})
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
