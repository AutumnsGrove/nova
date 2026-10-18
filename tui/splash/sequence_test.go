//go:build !notui

package splash

import (
	"image/color"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// sameColour compares two colours by their 8-bit components.
func sameColour(a, b color.Color) bool {
	ar, ag, ab := rgb8(a)
	br, bg, bb := rgb8(b)
	return ar == br && ag == bg && bb == ab
}

// shadowSet is the expected warm shadow cells for a wordmark.
func shadowSet(w wordmark) []point {
	var out []point
	for _, p := range w.pts {
		q := point{p.x + 1, p.y + 1}
		if !w.set[q] {
			out = append(out, q)
		}
	}
	return out
}

func TestSplash_SQN07_FirstFrame(t *testing.T) {
	cells := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", 0)
	if containsRune(cells, '╱') {
		t.Fatal("wordmark drawn on the first frame")
	}
	if containsRune(cells, '─') || containsRune(cells, '│') {
		t.Fatal("spike drawn on the first frame")
	}
	if containsRune(cells, '*') || containsRune(cells, ':') {
		t.Fatal("shell drawn on the first frame")
	}
	if !containsRune(cells, '◆') {
		t.Fatal("quiet star missing on the first frame")
	}
	found := false
	for y, row := range cells {
		for x, c := range row {
			if isStarField(x, y) && isStarGlyph(c.Ch) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no star field on the first frame")
	}
}

func TestSplash_SQN08_NameAssemblesLeftToRight(t *testing.T) {
	originX := fullGeom.wordX
	w := float64(fullGeom.word.w)
	counts := make([]int, 0, 3)
	for _, frac := range []float64{0.10, 0.20, 0.30} {
		cells := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(frac))
		pos := findRune(cells, '╱')
		rev := smoothstep(0.05, 0.34, frac)
		edge := originX + int(rev*1.25*w) + 1
		for _, p := range pos {
			if p.x > edge {
				t.Fatalf("at %.2f a slash at %d is past the leading edge %d", frac, p.x, edge)
			}
		}
		counts = append(counts, len(pos))
	}
	if !(counts[0] < counts[1] && counts[1] < counts[2]) {
		t.Fatalf("wordmark cell counts did not grow: %v", counts)
	}
}

func TestSplash_SQN09_NameCompleteAfterAssembly(t *testing.T) {
	cells := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(0.34))
	for _, p := range fullGeom.word.pts {
		if cells[fullGeom.wordY+p.y][fullGeom.wordX+p.x].Ch != '╱' {
			t.Fatalf("face cell (%d,%d) missing", p.x, p.y)
		}
	}
	for _, p := range shadowSet(fullGeom.word) {
		if cells[fullGeom.wordY+p.y][fullGeom.wordX+p.x].Ch != '╱' {
			t.Fatalf("shadow cell (%d,%d) missing", p.x, p.y)
		}
	}
}

func TestSplash_SQN10_IgnitionOverlapsAssembly(t *testing.T) {
	cells := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(0.20))
	total := len(fullGeom.word.pts) + len(shadowSet(fullGeom.word))
	if n := countRune(cells, '╱'); n == 0 || n >= total {
		t.Fatalf("word is not partly drawn: %d of %d cells", n, total)
	}
	if !containsRune(cells, '─') && !containsRune(cells, '│') {
		t.Fatal("no spike during ignition")
	}
	if !containsRune(cells, '✦') && !containsRune(cells, '◆') {
		t.Fatal("no ignition star")
	}
}

func TestSplash_SQN11_SpikesGrowThenEase(t *testing.T) {
	counts := make([]int, 0, 3)
	for _, frac := range []float64{0.12, 0.22, 0.30} {
		cells := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(frac))
		counts = append(counts, countRune(cells, '─'))
	}
	if counts[0] != 0 {
		t.Fatalf("spikes present at ignition start: %v", counts)
	}
	if !(counts[1] > counts[0] && counts[1] > counts[2] && counts[2] > 0) {
		t.Fatalf("spike lengths did not rise to a peak then fall: %v", counts)
	}
}

// shellExtent is the farthest non-star shell glyph from the star.
func shellExtent(cells [][]Cell) float64 {
	best := -1.0
	for y, row := range cells {
		for x, c := range row {
			if !isStarGlyph(c.Ch) && c.Ch != '*' && c.Ch != ':' && c.Ch != '·' {
				continue
			}
			if isStarField(x, y) {
				continue
			}
			d := math.Hypot(float64(x-fullGeom.starX)/2, float64(y-fullGeom.starY))
			if d > best {
				best = d
			}
		}
	}
	return best
}

func TestSplash_SQN12_ShellExpands(t *testing.T) {
	prev := -1.0
	for frac := 0.25; frac <= 0.62; frac += 0.02 {
		cells := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(frac))
		d := shellExtent(cells)
		if d < 0 {
			t.Fatalf("no shell glyph at %.2f", frac)
		}
		if prev >= 0 && d < prev-0.75 {
			t.Fatalf("shell moved closer at %.2f: %v after %v", frac, d, prev)
		}
		prev = d
	}
}

func TestSplash_SQN13_ShellCools(t *testing.T) {
	cool := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(0.28))
	found := false
	for y := range cool {
		for x := range cool[y] {
			c := cool[y][x]
			if isStarField(x, y) || (c.Ch != '*' && c.Ch != ':') {
				continue
			}
			r, _, b := rgbCell(c)
			if b <= r {
				t.Fatalf("early shell is not blue: r=%d b=%d", r, b)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no early shell band found")
	}
	warm := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(0.60))
	found = false
	for y := range warm {
		for x := range warm[y] {
			c := warm[y][x]
			if isStarField(x, y) || c.Ch != '·' {
				continue
			}
			d := math.Hypot(float64(x-fullGeom.starX)/2, float64(y-fullGeom.starY))
			if d < float64(fullGeom.rmax)-1.5 {
				continue
			}
			r, _, b := rgbCell(c)
			if r > b {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no cooled outer shell band found")
	}
}

func TestSplash_SQN14_StarGlyphByFlare(t *testing.T) {
	loud := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(0.22))
	if got := loud[fullGeom.starY][fullGeom.starX].Ch; got != '✦' {
		t.Fatalf("star at flare = %q, want ✦", got)
	}
	quiet := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(0.90))
	if got := quiet[fullGeom.starY][fullGeom.starX].Ch; got != '◆' {
		t.Fatalf("star at rest = %q, want ◆", got)
	}
}

func TestSplash_SQN15_HeldStateKeepsEverything(t *testing.T) {
	for _, d := range []time.Duration{Sequence, 20 * 1000 * 1000 * 1000, 10 * 60 * 1000 * 1000 * 1000} {
		cells := grid(t, 96, 21, tc(), colorprofile.TrueColor, "v1", d)
		for _, p := range fullGeom.word.pts {
			if cells[fullGeom.wordY+p.y][fullGeom.wordX+p.x].Ch != '╱' {
				t.Fatalf("at %v face cell (%d,%d) missing", d, p.x, p.y)
			}
		}
		for _, p := range shadowSet(fullGeom.word) {
			if cells[fullGeom.wordY+p.y][fullGeom.wordX+p.x].Ch != '╱' {
				t.Fatalf("at %v shadow cell (%d,%d) missing", d, p.x, p.y)
			}
		}
		text := flat(cells)
		if !strings.Contains(text, "terminal coding agent") {
			t.Fatalf("at %v missing the tagline", d)
		}
		if !containsRune(cells, '◆') {
			t.Fatalf("at %v star missing", d)
		}
		if !containsRune(cells, '─') || !containsRune(cells, '│') {
			t.Fatalf("at %v spikes missing", d)
		}
		if !containsRune(cells, '●') {
			t.Fatalf("at %v companion missing", d)
		}
		if shellExtent(cells) < 0 {
			t.Fatalf("at %v shell missing", d)
		}
	}
}

func TestSplash_SQN16_HeldShellStaysFaint(t *testing.T) {
	cells := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", 20*1000*1000*1000)
	bg := role(tc(), "bg")
	found := false
	for y := range cells {
		for x := range cells[y] {
			c := cells[y][x]
			if isStarField(x, y) || (c.Ch != '·' && c.Ch != '.') {
				continue
			}
			d := math.Hypot(float64(x-fullGeom.starX)/2, float64(y-fullGeom.starY))
			if math.Abs(d-float64(fullGeom.rmax)) > 0.75 {
				continue
			}
			if !sameColour(c.FG, bg) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("held shell ring is invisible")
	}
}

func TestSplash_SQN17_NameKeepsShimmering(t *testing.T) {
	a := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", 20*1000*1000*1000)
	b := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", 20*1000*1000*1000+400*1000*1000)
	pa, pb := findRune(a, '╱'), findRune(b, '╱')
	if len(pa) != len(pb) {
		t.Fatalf("wordmark cells moved: %d then %d", len(pa), len(pb))
	}
	for i := range pa {
		if pa[i] != pb[i] {
			t.Fatalf("wordmark cell moved: %v then %v", pa[i], pb[i])
		}
	}
	diff := false
	for i := range pa {
		if !sameColour(a[pa[i].y][pa[i].x].FG, b[pb[i].y][pb[i].x].FG) {
			diff = true
		}
	}
	if !diff {
		t.Fatal("no wordmark cell changed colour")
	}
}

func TestSplash_SQN18_StarsTwinkleInPlace(t *testing.T) {
	a := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", 20*1000*1000*1000)
	b := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", 20*1000*1000*1000+500*1000*1000)
	pa, pb := starCells(a), starCells(b)
	if len(pa) != len(pb) {
		t.Fatalf("star cells changed: %d then %d", len(pa), len(pb))
	}
	diff := false
	for i := range pa {
		if pa[i] != pb[i] {
			t.Fatalf("star moved: %v then %v", pa[i], pb[i])
		}
		if !sameColour(a[pa[i].y][pa[i].x].FG, b[pb[i].y][pb[i].x].FG) {
			diff = true
		}
	}
	if !diff {
		t.Fatal("no star changed colour")
	}
}

// starCells lists the star-field cells in row-major order.
func starCells(cells [][]Cell) []point {
	var out []point
	for y, row := range cells {
		for x, c := range row {
			if isStarField(x, y) && isStarGlyph(c.Ch) {
				out = append(out, point{x, y})
			}
		}
	}
	return out
}

func TestSplash_SQN19_CompanionOrbits(t *testing.T) {
	a := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", 20*1000*1000*1000)
	b := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", 20*1000*1000*1000+500*1000*1000)
	pa, pb := findRune(a, '●'), findRune(b, '●')
	if len(pa) != 1 || len(pb) != 1 {
		t.Fatalf("companion count = %d, %d", len(pa), len(pb))
	}
	for _, p := range append(pa, pb...) {
		if dx, dy := p.x-fullGeom.starX, p.y-fullGeom.starY; dx < -4 || dx > 4 || dy < -2 || dy > 2 {
			t.Fatalf("companion at %v is outside the orbit", p)
		}
	}
	if pa[0] == pb[0] {
		t.Fatal("companion did not move")
	}
}

func TestSplash_SQN20_SlashGlyphAndShadow(t *testing.T) {
	st := tc()
	cells := grid(t, 96, 21, st, colorprofile.TrueColor, "", at(1))
	for _, p := range fullGeom.word.pts {
		if cells[fullGeom.wordY+p.y][fullGeom.wordX+p.x].Ch != '╱' {
			t.Fatalf("face cell (%d,%d) is not a slash", p.x, p.y)
		}
	}
	want := blend(role(st, "bg"), role(st, "warm"), 0.42)
	for _, p := range shadowSet(fullGeom.word) {
		cell := cells[fullGeom.wordY+p.y][fullGeom.wordX+p.x]
		if cell.Ch != '╱' {
			t.Fatalf("shadow cell (%d,%d) is not a slash", p.x, p.y)
		}
		if !sameColour(cell.FG, want) {
			t.Fatalf("shadow cell (%d,%d) is not warm at 0.42", p.x, p.y)
		}
	}
}

func TestSplash_SQN21_TaglineAndVersion(t *testing.T) {
	cells := grid(t, 96, 21, tc(), colorprofile.TrueColor, "v1.2.3", at(1))
	lines := rows(cells)
	tag := lines[fullGeom.tagRow]
	if !strings.Contains(tag, "terminal coding agent  v1.2.3") {
		t.Fatalf("tagline row = %q", tag)
	}
	if at := strings.Index(tag, "terminal coding agent"); at != fullGeom.wordX {
		t.Fatalf("tagline starts at %d, want %d", at, fullGeom.wordX)
	}
}

func TestSplash_SQN55_NoDismissalPrompt(t *testing.T) {
	// A key no longer dismisses the splash, so no tier may advertise one.
	for _, size := range []struct{ w, h int }{{96, 21}, {64, 13}, {46, 10}, {40, 5}, {20, 5}} {
		cells := grid(t, size.w, size.h, tc(), colorprofile.TrueColor, "v1", at(1))
		if strings.Contains(flat(cells), "press any key") {
			t.Fatalf("%dx%d still draws a dismissal prompt:\n%s", size.w, size.h, flat(cells))
		}
	}
	mono := styles.Build(styles.Mono(), colorprofile.TrueColor)
	plain := grid(t, 96, 21, mono, colorprofile.TrueColor, "v1", at(1))
	if strings.Contains(flat(plain), "press any key") {
		t.Fatalf("the plain tier still draws a dismissal prompt:\n%s", flat(plain))
	}
}

func TestSplash_SQN22_TaglineFadesIn(t *testing.T) {
	early := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(0.30))
	if strings.Contains(flat(early), "terminal coding agent") {
		t.Fatal("tagline present before its fade")
	}
	late := grid(t, 96, 21, tc(), colorprofile.TrueColor, "", at(0.60))
	if !strings.Contains(flat(late), "terminal coding agent") {
		t.Fatal("tagline absent after its fade")
	}
}

func TestSplash_SQN27_Compact(t *testing.T) {
	cells := grid(t, 64, 13, tc(), colorprofile.TrueColor, "v1", at(1))
	for _, p := range thinWord.pts {
		if cells[compactGeom.wordY+p.y][compactGeom.wordX+p.x].Ch != '╱' {
			t.Fatalf("compact face cell (%d,%d) missing", p.x, p.y)
		}
	}
	if cells[compactGeom.starY][compactGeom.starX].Ch != '◆' {
		t.Fatal("compact star missing")
	}
	if !strings.Contains(rows(cells)[compactGeom.tagRow], "terminal coding agent") {
		t.Fatal("compact tagline missing")
	}
	// The held shell sits at Rmax = 5 rows.
	found := false
	for y := range cells {
		for x := range cells[y] {
			if isStarField(x, y) || isStarGlyph(cells[y][x].Ch) {
				continue
			}
			d := math.Hypot(float64(x-compactGeom.starX)/2, float64(y-compactGeom.starY))
			if math.Abs(d-float64(compactGeom.rmax)) <= 0.75 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("compact shell missing at Rmax")
	}
}

func TestSplash_SQN28_ThinHasNoOutburst(t *testing.T) {
	for _, d := range []time.Duration{0, at(0.5), at(1)} {
		cells := grid(t, 46, 10, tc(), colorprofile.TrueColor, "", d)
		for _, p := range thinWord.pts {
			if cells[2+p.y][p.x].Ch != '╱' {
				t.Fatalf("at %v thin face cell (%d,%d) missing", d, p.x, p.y)
			}
		}
		for _, r := range []rune{'─', '│', '●', '*', ':'} {
			if containsRune(cells, r) {
				t.Fatalf("at %v thin tier emitted %q", d, r)
			}
		}
	}
}

func TestSplash_SQN29_Lockup(t *testing.T) {
	cells := grid(t, 40, 5, tc(), colorprofile.TrueColor, "", at(0.4))
	const text = "╱╱╱ nova"
	runes := []rune(text)
	x0, y0 := (40-len(runes))/2, (5-1)/2
	row := []rune(rows(cells)[y0])
	got := string(row[x0 : x0+len(runes)])
	if want := text; got != want {
		t.Fatalf("lockup row = %q, want %q at %d", got, want, x0)
	}
}

func TestSplash_SQN57_BannerHeightPicksTheTier(t *testing.T) {
	cases := []struct{ w, budget, want int }{
		{120, 54, 21}, // Full
		{80, 18, 13},  // Compact: Full is too wide
		{50, 12, 10},  // Thin
		{45, 6, 5},    // Lockup: too narrow for Thin
		{30, 20, 5},   // narrow: the Plain tier in a lockup-height banner
		{120, 4, 0},   // no room even for the lockup
	}
	for _, c := range cases {
		if got := BannerHeight(c.w, c.budget); got != c.want {
			t.Fatalf("BannerHeight(%d, %d) = %d, want %d", c.w, c.budget, got, c.want)
		}
	}
}
