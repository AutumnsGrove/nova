//go:build !notui

package splash

import (
	"image/color"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// tc is the brand's dark theme at truecolor, the tier tests' usual styles.
func tc() styles.Styles { return styles.Build(styles.Dark(), colorprofile.TrueColor) }

// at converts a fraction of Sequence to a duration.
func at(frac float64) time.Duration { return time.Duration(frac * float64(Sequence)) }

// grid is a Cells call that must succeed.
func grid(t *testing.T, w, h int, st styles.Styles, p colorprofile.Profile, version string, d time.Duration) [][]Cell {
	t.Helper()
	cells, _, ok := Cells(w, h, st, p, version, d)
	if !ok {
		t.Fatalf("Cells(%d,%d) not ok", w, h)
	}
	return cells
}

// rows renders each grid row as a plain string.
func rows(cells [][]Cell) []string {
	out := make([]string, len(cells))
	for y, row := range cells {
		var b strings.Builder
		for _, c := range row {
			b.WriteRune(c.Ch)
		}
		out[y] = b.String()
	}
	return out
}

// flat joins the plain rows.
func flat(cells [][]Cell) string { return strings.Join(rows(cells), "\n") }

// countRune counts a glyph across the grid.
func countRune(cells [][]Cell, r rune) int {
	n := 0
	for _, row := range cells {
		for _, c := range row {
			if c.Ch == r {
				n++
			}
		}
	}
	return n
}

// findRune lists every cell holding a glyph.
func findRune(cells [][]Cell, r rune) []point {
	var out []point
	for y, row := range cells {
		for x, c := range row {
			if c.Ch == r {
				out = append(out, point{x, y})
			}
		}
	}
	return out
}

// containsRune reports whether a glyph appears anywhere.
func containsRune(cells [][]Cell, r rune) bool { return countRune(cells, r) > 0 }

// rgbCell reduces a cell colour to opaque 8-bit components.
func rgbCell(cell Cell) (uint8, uint8, uint8) {
	r, g, b, _ := cell.FG.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

// isStarField reports whether the star field places a star at (x, y).
func isStarField(x, y int) bool { return hash(x, y, 917) < 0.055 }

// isStarGlyph reports whether a glyph is one the star field can emit.
func isStarGlyph(r rune) bool { return r == '·' || r == '.' || r == '+' }

func TestSplash_SQN01_TierBySize(t *testing.T) {
	cases := []struct {
		w, h int
		want Tier
		ok   bool
	}{
		{120, 40, Full, true},
		{96, 21, Full, true},
		{95, 21, Compact, true},
		{64, 13, Compact, true},
		{63, 13, Thin, true},
		{46, 10, Thin, true},
		{45, 10, Lockup, true},
		{40, 5, Lockup, true},
		{39, 5, Plain, true},
		{20, 3, Plain, true},
		{19, 3, Plain, false},
	}
	for _, c := range cases {
		out, tier, ok := Render(c.w, c.h, tc(), colorprofile.TrueColor, "", 0)
		if ok != c.ok {
			t.Fatalf("%dx%d: ok = %v, want %v", c.w, c.h, ok, c.ok)
		}
		if tier != c.want {
			t.Fatalf("%dx%d: tier = %v, want %v", c.w, c.h, tier, c.want)
		}
		if !ok && out != "" {
			t.Fatalf("%dx%d: not ok but output %q", c.w, c.h, out)
		}
	}
}

func TestSplash_SQN02_TierNeedsBothDimensions(t *testing.T) {
	for _, c := range []struct{ w, h int }{{200, 20}, {96, 20}} {
		_, tier, ok := Render(c.w, c.h, tc(), colorprofile.TrueColor, "", 0)
		if !ok || tier != Compact {
			t.Fatalf("%dx%d: tier = %v ok=%v, want Compact", c.w, c.h, tier, ok)
		}
	}
}

func TestSplash_SQN03_Below256IsPlain(t *testing.T) {
	st := styles.Build(styles.Dark(), colorprofile.ANSI)
	out, tier, ok := Render(120, 40, st, colorprofile.ANSI, "", 0)
	if !ok || tier != Plain {
		t.Fatalf("tier = %v ok=%v, want Plain", tier, ok)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("plain output has escape codes: %q", out)
	}
}

func TestSplash_SQN04_MonoIsPlain(t *testing.T) {
	st := styles.Build(styles.Mono(), colorprofile.TrueColor)
	_, tier, ok := Render(120, 40, st, colorprofile.TrueColor, "", 0)
	if !ok || tier != Plain {
		t.Fatalf("tier = %v ok=%v, want Plain", tier, ok)
	}
}

func TestSplash_SQN05_NeverExceedsArea(t *testing.T) {
	for w := 21; w <= 120; w += 7 {
		for h := 3; h <= 40; h += 5 {
			for _, frac := range []float64{0, 0.2, 0.5, 1, 2, 7} {
				out, _, ok := Render(w, h, tc(), colorprofile.TrueColor, "v1", at(frac))
				if !ok {
					continue
				}
				lines := strings.Split(out, "\n")
				if len(lines) != h {
					t.Fatalf("%dx%d: %d lines, want %d", w, h, len(lines), h)
				}
				for _, l := range lines {
					if got := ansi.StringWidth(l); got > w {
						t.Fatalf("%dx%d: line is %d wide", w, h, got)
					}
				}
			}
		}
	}
}

func TestSplash_SQN06_Deterministic(t *testing.T) {
	a, _, _ := Render(120, 40, tc(), colorprofile.TrueColor, "v1.2.3", at(0.37))
	b, _, _ := Render(120, 40, tc(), colorprofile.TrueColor, "v1.2.3", at(0.37))
	if a != b {
		t.Fatal("splash is not deterministic")
	}
}

func TestSplash_SQN23_ColoursFromTheme(t *testing.T) {
	dark := tc()
	other := styles.Dark()
	other.Colors["spark"] = "#ff00ff"
	other.Colors["warm"] = "#00ff00"
	otherStyles := styles.Build(other, colorprofile.TrueColor)
	a, _, _ := Render(96, 21, dark, colorprofile.TrueColor, "", at(0.5))
	b, _, _ := Render(96, 21, otherStyles, colorprofile.TrueColor, "", at(0.5))
	if a == b {
		t.Fatal("a different spark colour did not change the output")
	}
	if findLiteralColour(t) {
		t.Fatal("tui/splash source contains a literal colour")
	}
}

// findLiteralColour scans the package's non-test sources for hex colour
// literals, which must live in the theme, not here.
func findLiteralColour(t *testing.T) bool {
	t.Helper()
	re := regexp.MustCompile(`#[0-9a-fA-F]{6}`)
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if re.Match(data) {
			t.Fatalf("%s contains a hex colour literal", name)
		}
	}
	return false
}

func TestSplash_SQN24_ProfileDegradation(t *testing.T) {
	st := styles.Build(styles.Dark(), colorprofile.ANSI256)
	out, tier, ok := Render(96, 21, st, colorprofile.ANSI256, "", at(0.5))
	if !ok || tier != Full {
		t.Fatalf("tier = %v ok=%v", tier, ok)
	}
	if strings.Contains(out, "38;2;") {
		t.Fatal("truecolor escape in a 256-colour render")
	}
	if !strings.Contains(out, "38;5;") {
		t.Fatal("no 256-colour escape in a 256-colour render")
	}
}

func TestSplash_SQN25_SceneIsCentred(t *testing.T) {
	cells := grid(t, 120, 40, tc(), colorprofile.TrueColor, "", at(1))
	offX, offY := (120-fullGeom.w)/2, (40-fullGeom.h)/2
	if got := cells[offY+fullGeom.wordY][offX+fullGeom.wordX].Ch; got != '╱' {
		t.Fatalf("word origin cell = %q, want a slash", got)
	}
}

func TestSplash_SQN26_StarsStableUnderResize(t *testing.T) {
	a := grid(t, 100, 40, tc(), colorprofile.TrueColor, "", at(1))
	b := grid(t, 101, 40, tc(), colorprofile.TrueColor, "", at(1))
	for y := range a {
		for x := range a[y] {
			if !isStarField(x, y) {
				continue
			}
			if b[y][x].Ch != a[y][x].Ch {
				t.Fatalf("star at (%d,%d) moved: %q then %q", x, y, a[y][x].Ch, b[y][x].Ch)
			}
		}
	}
}

func TestSplash_SQN30_PlainIsStatic(t *testing.T) {
	st := styles.Build(styles.Dark(), colorprofile.ANSI)
	a, _, _ := Render(80, 24, st, colorprofile.ANSI, "v1", 0)
	b, _, _ := Render(80, 24, st, colorprofile.ANSI, "v1", time.Hour)
	if a != b {
		t.Fatal("plain splash changes over time")
	}
}

func TestSplash_SQN31_GlyphWidths(t *testing.T) {
	seen := map[rune]bool{}
	for _, size := range [][2]int{{96, 21}, {64, 13}, {46, 10}, {40, 5}} {
		for _, frac := range []float64{0, 0.12, 0.22, 0.5, 1, 5} {
			cells, _, ok := Cells(size[0], size[1], tc(), colorprofile.TrueColor, "v1", at(frac))
			if !ok {
				t.Fatalf("%v not ok", size)
			}
			for _, row := range cells {
				for _, c := range row {
					seen[c.Ch] = true
				}
			}
		}
	}
	for r := range seen {
		if r == ' ' {
			continue
		}
		if w := ansi.StringWidth(string(r)); w != 1 {
			t.Fatalf("glyph %q has width %d", r, w)
		}
	}
}

func TestSplash_SQN32_Goldens(t *testing.T) {
	var b strings.Builder
	write := func(label string, w, h int, version string, d time.Duration) {
		out, _, _ := Render(w, h, tc(), colorprofile.TrueColor, version, d)
		b.WriteString("--- " + label + " ---\n")
		b.WriteString(out)
		b.WriteString("\n")
	}
	write("full-0", 96, 21, "", 0)
	write("full-020", 96, 21, "", at(0.20))
	write("full-050", 96, 21, "", at(0.50))
	write("full-held", 96, 21, "v1.2.3", at(1))
	write("compact-held", 64, 13, "", at(1))
	write("thin", 46, 10, "", at(0.4))
	write("lockup", 40, 5, "", at(0.4))
	st := styles.Build(styles.Dark(), colorprofile.ANSI)
	plain, _, _ := Render(40, 8, st, colorprofile.ANSI, "v1.2.3", 0)
	b.WriteString("--- plain ---\n" + plain + "\n")
	golden.RequireEqual(t, b.String())
}

func TestSplash_SQN40_GeneratedFileGone(t *testing.T) {
	if _, err := os.Stat("mark_gen.go"); !os.IsNotExist(err) {
		t.Fatalf("mark_gen.go still present: %v", err)
	}
	data, err := os.ReadFile("splash.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "go:generate") {
		t.Fatal("splash.go still has a go:generate line")
	}
}

func TestSplash_SQN41_CellsAndRenderAgree(t *testing.T) {
	for _, size := range [][2]int{{96, 21}, {64, 13}, {46, 10}, {40, 5}, {30, 8}} {
		for _, frac := range []float64{0, 0.2, 0.5, 1, 3} {
			out, _, ok := Render(size[0], size[1], tc(), colorprofile.TrueColor, "v1", at(frac))
			if !ok {
				continue
			}
			cells := grid(t, size[0], size[1], tc(), colorprofile.TrueColor, "v1", at(frac))
			if got, want := ansi.Strip(out), flat(cells); got != want {
				t.Fatalf("%v @%v:\n got %q\nwant %q", size, frac, got, want)
			}
		}
	}
}

func TestProperty_SQN_Bounds(t *testing.T) {
	r := rand.New(rand.NewSource(21))
	for i := 0; i < 300; i++ {
		w := 1 + r.Intn(140)
		h := 1 + r.Intn(50)
		d := time.Duration(r.Int63n(int64(30 * time.Second)))
		out, _, ok := Render(w, h, tc(), colorprofile.TrueColor, "", d)
		if !ok {
			if out != "" {
				t.Fatalf("%dx%d: not ok but non-empty", w, h)
			}
			continue
		}
		lines := strings.Split(out, "\n")
		if len(lines) != h {
			t.Fatalf("%dx%d: %d lines", w, h, len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Fatalf("%dx%d: line too wide", w, h)
			}
		}
	}
}

func FuzzRender(f *testing.F) {
	f.Add(96, 21, int64(0), uint8(0), "v1")
	f.Add(0, 0, int64(-1), uint8(5), "日本語")
	f.Add(20, 3, int64(999999999999), uint8(3), "\x00\x1b[31m")
	profiles := []colorprofile.Profile{colorprofile.ASCII, colorprofile.ANSI, colorprofile.ANSI256, colorprofile.TrueColor}
	f.Fuzz(func(t *testing.T, w, h int, ns int64, pi uint8, version string) {
		p := profiles[int(pi)%len(profiles)]
		out, _, ok := Render(w, h, tc(), p, version, time.Duration(ns))
		if !ok {
			return
		}
		lines := strings.Split(out, "\n")
		if len(lines) != h {
			t.Fatalf("got %d lines, want %d for %dx%d", len(lines), h, w, h)
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Fatalf("line wider than %d", w)
			}
		}
	})
}

// blendWarm is the shadow colour at the given opacity, for sequence tests.
func blendWarm(st styles.Styles, opacity float64) color.Color {
	return blend(role(st, "bg"), role(st, "warm"), opacity)
}
