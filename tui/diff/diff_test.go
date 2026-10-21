//go:build !notui

package diff

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

func mono() styles.Styles   { return styles.Build(styles.Mono(), colorprofile.ASCII) }
func colors() styles.Styles { return styles.Build(styles.Dark(), colorprofile.TrueColor) }

const sample = "@@ -1,3 +1,3 @@\n package main\n-import \"old\"\n+import \"new\"\n \n func main() {}\n"

func TestDiff_DFV01_AddRemoveContext(t *testing.T) {
	monoOut := Render(sample, "main.go", 60, mono(), Options{})
	for _, want := range []string{"+import \"new\"", "-import \"old\"", " package main"} {
		if !strings.Contains(monoOut, want) {
			t.Fatalf("mono diff missing %q:\n%s", want, monoOut)
		}
	}
	if hasColour(monoOut) {
		t.Fatalf("mono diff has colour escapes:\n%q", monoOut)
	}
	colorOut := Render(sample, "main.go", 60, colors(), Options{})
	if !strings.Contains(colorOut, "\x1b[") {
		t.Fatalf("colour diff has no styling:\n%q", colorOut)
	}
}

func TestDiff_DFV02_GutterNumbers(t *testing.T) {
	lines := parse("@@ -1,2 +1,2 @@\n a\n-b\n+c\n@@ -10,1 +10,1 @@\n-d\n+e\n")
	var old, new []int
	for _, l := range lines {
		if l.kind == kContext || l.kind == kRemove {
			old = append(old, l.old)
		}
		if l.kind == kContext || l.kind == kAdd {
			new = append(new, l.new)
		}
	}
	if strings.Trim(strings.Join(itoas(old), ","), ",") == "" {
		t.Fatalf("no old numbers: %+v", lines)
	}
	want := "10"
	out := Render("@@ -10,1 +10,1 @@\n-d\n+e\n", "x.go", 40, mono(), Options{ShowNumbers: true})
	if !strings.Contains(out, want) {
		t.Fatalf("second hunk numbers missing from:\n%s", out)
	}
}

func TestDiff_DFV03_HunkHeader(t *testing.T) {
	out := Render(sample, "main.go", 60, mono(), Options{})
	if !strings.Contains(out, "@@") {
		t.Fatalf("hunk header missing:\n%s", out)
	}
}

func TestDiff_DFV04_WrapWithArrow(t *testing.T) {
	long := "+" + strings.Repeat("x", 80)
	out := Render(long+"\n", "f", 20, mono(), Options{Wrap: true})
	if !strings.Contains(out, "↪") {
		t.Fatalf("no continuation marker:\n%s", out)
	}
}

func TestDiff_DFV05_TruncateWithoutWrap(t *testing.T) {
	long := "+" + strings.Repeat("x", 80)
	out := Render(long+"\n", "f", 20, mono(), Options{})
	if !strings.Contains(out, "…") {
		t.Fatalf("no truncation marker:\n%s", out)
	}
}

func TestDiff_DFV06_TabsAndCRLF(t *testing.T) {
	out := Render("+\tfoo\r\n", "f", 40, mono(), Options{})
	if strings.ContainsAny(out, "\t\r") {
		t.Fatalf("tab or CR survived: %q", out)
	}
}

func TestDiff_DFV07_WideCharacters(t *testing.T) {
	out := Render("+世界世界世界\n", "f", 10, mono(), Options{})
	for _, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) > 10 {
			t.Fatalf("line %q is %d wide", l, ansi.StringWidth(l))
		}
	}
}

func TestDiff_DFV08_MaxLines(t *testing.T) {
	var b strings.Builder
	b.WriteString("@@ -1,1 +1,1 @@\n")
	for i := 0; i < 100; i++ {
		b.WriteString("+line\n")
	}
	out := Render(b.String(), "f", 40, mono(), Options{MaxLines: 10})
	if !strings.Contains(out, "more lines") {
		t.Fatalf("no cap marker:\n%s", out)
	}
}

func TestDiff_DFV09_NoHunks(t *testing.T) {
	if got := Render(" context only\n", "f", 40, mono(), Options{}); got != "(no changes)" {
		t.Fatalf("got %q", got)
	}
	if got := Render("", "f", 40, mono(), Options{}); got != "(no changes)" {
		t.Fatalf("empty diff = %q", got)
	}
}

func TestDiff_DFV10_Malformed(t *testing.T) {
	got := Render("this is not a diff\nsecond line", "f", 40, mono(), Options{})
	if !strings.Contains(got, "this is not a diff") {
		t.Fatalf("malformed text lost: %q", got)
	}
}

func TestDiff_DFV11_NoNewlineMarker(t *testing.T) {
	out := Render("-old\n\\ No newline at end of file\n+new\n", "f", 60, mono(), Options{})
	if !strings.Contains(out, "No newline at end of file") {
		t.Fatalf("marker missing:\n%s", out)
	}
}

func TestDiff_DFV12_WidthUnder20(t *testing.T) {
	out := Render(sample, "main.go", 10, mono(), Options{ShowNumbers: true})
	for _, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) > 10 {
			t.Fatalf("line %q exceeds width", l)
		}
	}
}

func TestDiff_DFV13_NoColourMarkersOnly(t *testing.T) {
	out := Render(sample, "main.go", 60, mono(), Options{})
	if hasColour(out) {
		t.Fatalf("no-colour diff has colour escapes: %q", out)
	}
	if !strings.Contains(out, "+") || !strings.Contains(out, "-") {
		t.Fatalf("markers missing:\n%s", out)
	}
}

// hasColour reports whether s contains a colour SGR sequence. Bold, dim, and
// reverse are allowed.
func hasColour(s string) bool {
	for _, marker := range []string{"38;", "48;", "\x1b[3", "\x1b[4", "\x1b[9"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func TestDiff_DFV14_SyntaxHighlight(t *testing.T) {
	out := Render("+package main\n+func f() { return }\n", "main.go", 80, colors(), Options{})
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("no highlighting:\n%q", out)
	}
}

func TestDiff_DFV15_Deterministic(t *testing.T) {
	a := Render(sample, "main.go", 60, colors(), Options{ShowNumbers: true, Wrap: true})
	b := Render(sample, "main.go", 60, colors(), Options{ShowNumbers: true, Wrap: true})
	if a != b {
		t.Fatal("diff rendering is not deterministic")
	}
}

func itoas(ns []int) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = string(rune('0' + n%10))
	}
	return out
}

// FuzzDiffRender asserts any text at any width never panics and never renders
// a line wider than the width.
func FuzzDiffRender(f *testing.F) {
	f.Add("@@ -1 +1 @@\n-a\n+b\n", 40)
	f.Add("hello\nworld", 10)
	f.Add("+\twide 世界\n", 3)
	f.Fuzz(func(t *testing.T, text string, width int) {
		if width < 1 {
			width = 1
		}
		st := styles.Build(styles.Dark(), colorprofile.TrueColor)
		out := Render(text, "x.go", width, st, Options{Wrap: width%2 == 0, ShowNumbers: true, MaxLines: 20})
		for _, l := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(l); w > width {
				t.Fatalf("width %d line %q is %d wide", width, l, w)
			}
		}
	})
}

// A diff is built from file contents, so every line kind (hunk headers carry a
// function name from the file, and a non-diff text is drawn as it came) has to
// reach the terminal without bytes it acts on, in colour and without.
func TestDiff_DFV16_NoControlBytesInAnyLineKind(t *testing.T) {
	hostile := "\x1b]0;pwned\x07\x1b[2J‮"
	diff := "--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,2 @@ func f" + hostile + "\n-old" + hostile + "\n+new" + hostile + "\n ctx" + hostile + "\n"
	for name, st := range map[string]styles.Styles{"mono": mono(), "colour": styles.Build(styles.Dark(), colorprofile.TrueColor)} {
		for _, input := range []string{diff, "plain text" + hostile} {
			out := Render(input, "x.go", 120, st, Options{})
			if strings.Contains(out, "\x1b]") || strings.Contains(out, "[2J") || strings.ContainsAny(out, "\x07‮") {
				t.Errorf("%s: control bytes survived: %q", name, out)
			}
		}
	}
}
