//go:build !notui

package splash

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/AutumnsGrove/nova/tui/styles"
)

func truecolor() styles.Styles { return styles.Build(styles.Dark(), colorprofile.TrueColor) }
func nocolor() styles.Styles   { return styles.Build(styles.Mono(), colorprofile.ASCII) }

func TestSplash_SPL01_Levels(t *testing.T) {
	cases := []struct {
		w, h int
		want Level
	}{
		{120, 60, Full},
		{100, 30, Half},
		{60, 20, Quarter},
		{30, 15, Plain},
	}
	for _, c := range cases {
		_, lvl, ok := Render(c.w, c.h, truecolor(), colorprofile.TrueColor, "0.1")
		if !ok {
			t.Fatalf("%dx%d: not ok", c.w, c.h)
		}
		if lvl != c.want {
			t.Fatalf("%dx%d: level = %v, want %v", c.w, c.h, lvl, c.want)
		}
	}
}

func TestSplash_SPL02_Margin(t *testing.T) {
	if _, lvl, _ := Render(99, 53, truecolor(), colorprofile.TrueColor, "0.1"); lvl != Full {
		t.Fatalf("exact full size gave %v, want Full", lvl)
	}
	if _, lvl, _ := Render(98, 53, truecolor(), colorprofile.TrueColor, "0.1"); lvl != Half {
		t.Fatalf("one below full width gave %v, want Half", lvl)
	}
}

func TestSplash_SPL03_TooSmall(t *testing.T) {
	out, _, ok := Render(20, 10, truecolor(), colorprofile.TrueColor, "0.1")
	if ok || out != "" {
		t.Fatalf("small splash = %q, ok=%v", out, ok)
	}
}

func TestSplash_SPL04_NeverExceedsBounds(t *testing.T) {
	for w := 1; w <= 200; w += 7 {
		for h := 1; h <= 80; h += 5 {
			out, _, _ := Render(w, h, truecolor(), colorprofile.TrueColor, "0.1")
			lines := strings.Split(out, "\n")
			if out == "" {
				continue
			}
			if len(lines) > h {
				t.Fatalf("%dx%d: %d lines", w, h, len(lines))
			}
			for _, l := range lines {
				if ansi.StringWidth(l) > w {
					t.Fatalf("%dx%d: line %q is %d wide", w, h, l, ansi.StringWidth(l))
				}
			}
		}
	}
}

func TestSplash_SPL05_HalfBlockRows(t *testing.T) {
	out, lvl, _ := Render(120, 60, truecolor(), colorprofile.TrueColor, "0.1")
	if lvl != Full {
		t.Fatalf("level = %v", lvl)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 49 {
		t.Fatalf("full mark has %d lines, want 49", len(lines))
	}
	if !strings.ContainsAny(out, "▀▄█") {
		t.Fatal("no half-block characters")
	}
}

func TestSplash_SPL06_ColourByRole(t *testing.T) {
	out, _, _ := Render(120, 60, truecolor(), colorprofile.TrueColor, "0.1")
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("truecolor splash has no colour")
	}
	if !strings.Contains(out, "38;2;") {
		t.Fatalf("no truecolor sequence")
	}
}

func TestSplash_SPL07_NoColour(t *testing.T) {
	for _, p := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.ASCII} {
		out, lvl, ok := Render(120, 60, nocolor(), p, "0.1")
		if !ok || lvl != Plain {
			t.Fatalf("profile %v: level = %v", p, lvl)
		}
		if !strings.Contains(out, "*") {
			t.Fatalf("profile %v: no ASCII star", p)
		}
		if strings.Contains(out, "\x1b[") {
			t.Fatalf("profile %v: colour escapes present", p)
		}
	}
}

func TestSplash_SPL08_Caption(t *testing.T) {
	out, _, _ := Render(80, 40, nocolor(), colorprofile.ASCII, "1.2.3")
	for _, want := range []string{"nova 1.2.3", "press any key"} {
		if !strings.Contains(out, want) {
			t.Fatalf("caption missing %q:\n%s", want, out)
		}
	}
}

func TestSplash_SPL09_Deterministic(t *testing.T) {
	a, _, _ := Render(120, 60, truecolor(), colorprofile.TrueColor, "0.1")
	b, _, _ := Render(120, 60, truecolor(), colorprofile.TrueColor, "0.1")
	if a != b {
		t.Fatal("splash is not deterministic")
	}
}

func TestSplash_SPL10_Golden(t *testing.T) {
	var b strings.Builder
	for _, c := range []struct{ w, h int }{{120, 60}, {100, 30}, {60, 20}, {30, 15}} {
		out, lvl, _ := Render(c.w, c.h, truecolor(), colorprofile.TrueColor, "0.1")
		b.WriteString("--- " + lvl.String() + " ---\n" + out + "\n")
	}
	golden.RequireEqual(t, b.String())
}

// String names a level for golden headers.
func (l Level) String() string {
	switch l {
	case Full:
		return "Full"
	case Half:
		return "Half"
	case Quarter:
		return "Quarter"
	default:
		return "Plain"
	}
}

func TestSplash_SPL11_GeneratedCurrent(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
	tmp := filepath.Join(t.TempDir(), "mark_gen.go")
	cmd := exec.Command("go", "run", "../../assets/logo/gen", "-go", tmp)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("regenerate: %v\n%s", err, out)
	}
	want, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("mark_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("mark_gen.go is out of date; run go generate ./tui/splash")
	}
}
