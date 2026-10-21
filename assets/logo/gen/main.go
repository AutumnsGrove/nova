// Command gen writes the Nova logo as SVG files.
//
// There are two locked marks, both built from square cells so they can later be
// rendered as terminal half-blocks for the TUI splash screen:
//
//   - full: the expanded mark (a 97 by 97 cell grid). A cell star with long
//     diffraction spikes at the centre, a two-armed spiral, layered broken
//     shells of ejected gas, thin filaments, and a warm companion star.
//   - min: the minimal mark (a 49 by 49 cell grid). The same star and spiral
//     without the shells, spikes, or filaments. Used for icons and small sizes.
//
// Usage: go run ./assets/logo/gen [-out assets/logo]
package main

import (
	"flag"
	"fmt"
	"go/format"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	unit = 5.0  // cell pitch in canvas units
	tilt = -0.6 // tilt of the whole disc, in radians
)

type role int

const (
	roleSpark role = iota // primary star, first arm, inner shell
	roleInk               // second arm, dust, middle shell
	roleCore              // star centres and the companion
	roleWarm              // outer shell and filaments
	numRoles
)

type cell struct {
	i, j int
	o    float64
	r    role
}

type grid struct {
	half  int // cells run from -half to +half on both axes
	order []string
	m     map[string]cell
}

func newGrid(half int) *grid { return &grid{half: half, m: map[string]cell{}} }

// put places a cell. A soft put never overwrites an existing cell.
func (g *grid) put(i, j int, o float64, r role, soft bool) {
	if abs(i) > g.half || abs(j) > g.half {
		return
	}
	k := fmt.Sprintf("%d,%d", i, j)
	if _, ok := g.m[k]; ok {
		if soft {
			return
		}
	} else {
		g.order = append(g.order, k)
	}
	g.m[k] = cell{i, j, o, r}
}

// rotate rotates (x, y) by a and rounds half up.
func rotate(x, y, a float64) (int, int) {
	return round(x*math.Cos(a) - y*math.Sin(a)), round(x*math.Sin(a) + y*math.Cos(a))
}

func round(x float64) int { return int(math.Floor(x + 0.5)) }

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// rng is mulberry32, seeded so the artwork is identical on every run.
func rng(seed uint32) func() float64 {
	return func() float64 {
		seed += 0x6D2B79F5
		t := (seed ^ (seed >> 15)) * (1 | seed)
		t = (t + (t^(t>>7))*(61|t)) ^ t
		return float64(t^(t>>14)) / 4294967296
	}
}

var diagonals = [4][2]int{{1, 1}, {-1, 1}, {1, -1}, {-1, -1}}
var axes = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// ---------- minimal mark ----------

func (g *grid) starMin(ci, cj, l int, arm, core role) {
	for d := 0; d <= l; d++ {
		o := 1.0
		if d > 1 {
			o = math.Max(0.05, math.Pow(1-float64(d-1)/float64(l), 1.25))
		}
		if d == 0 {
			g.put(ci, cj, o, core, false)
			continue
		}
		for _, a := range axes {
			g.put(ci+a[0]*d, cj+a[1]*d, o, arm, false)
		}
	}
	k := int(math.Floor(float64(l) / 2.4))
	for n := 1; n <= k; n++ {
		o := 0.62 * math.Pow(1-float64(n-1)/float64(k+1), 1.6)
		for _, d := range diagonals {
			g.put(ci+d[0]*n, cj+d[1]*n, o, arm, false)
		}
	}
}

func buildMin() *grid {
	g := newGrid(24)
	r := rng(11)
	var endI, endJ int
	for arm := 0; arm < 2; arm++ {
		for t := 0.0; t <= 5.6; t += 0.012 {
			rad := 3 + 3.9*t
			th := t*1.05 + float64(arm)*math.Pi
			i, j := rotate(rad*math.Cos(th), rad*math.Sin(th)*0.5, tilt)
			o := math.Max(0.1, 1-t/6.2) * 0.75
			rl := roleSpark
			if arm == 1 {
				rl = roleInk
			}
			g.put(i, j, o, rl, true)
			if r() < 0.2 {
				di := round((r() - 0.5) * 5)
				dj := round((r() - 0.5) * 3)
				g.put(i+di, j+dj, o*0.5, roleInk, true)
			}
			if arm == 0 && t >= 5.55 {
				endI, endJ = i, j
			}
		}
	}
	g.starMin(0, 0, 15, roleSpark, roleCore)
	g.starMin(endI, endJ, 4, roleCore, roleCore)
	return g
}

// ---------- full mark ----------

// starFull draws the large star: a long falloff, a 3 by 3 hot core, diagonals.
func (g *grid) starFull(ci, cj, l int, arm, core role, diag int) {
	for d := 0; d <= l; d++ {
		o := 1.0
		if d > 2 {
			o = math.Max(0.05, math.Pow(1-float64(d-2)/float64(l), 1.4))
		}
		if d == 0 {
			g.put(ci, cj, o, core, false)
			continue
		}
		rl := arm
		if d <= 1 {
			rl = core
		}
		for _, a := range axes {
			g.put(ci+a[0]*d, cj+a[1]*d, o, rl, false)
		}
	}
	for a := -1; a <= 1; a++ {
		for b := -1; b <= 1; b++ {
			if a == 0 && b == 0 {
				continue
			}
			o := 0.8
			if abs(a)+abs(b) == 1 {
				o = 1
			}
			g.put(ci+a, cj+b, o, core, false)
		}
	}
	k := diag
	if k < 0 {
		k = int(math.Floor(float64(l) / 2.2))
	}
	for n := 2; n <= k; n++ {
		o := 0.6 * math.Pow(1-float64(n-1)/float64(k+1), 1.5)
		for _, d := range diagonals {
			g.put(ci+d[0]*n, cj+d[1]*n, o, arm, false)
		}
	}
}

func (g *grid) halo(radius int, o float64, rl role) {
	for i := -radius; i <= radius; i++ {
		for j := -radius; j <= radius; j++ {
			d := math.Hypot(float64(i), float64(j))
			if d <= float64(radius) {
				g.put(i, j, o*(1-d/float64(radius+1)), rl, true)
			}
		}
	}
}

// spiral draws the two arms and returns the end cell of the first.
func (g *grid) spiral(seed uint32, tight, dust float64) (int, int) {
	r := rng(seed)
	var ei, ej int
	for arm := 0; arm < 2; arm++ {
		for t := 0.0; t <= 5.6; t += 0.005 {
			rad := 5.5 + 7.2*t
			th := t*tight + float64(arm)*math.Pi
			i, j := rotate(rad*math.Cos(th), rad*math.Sin(th)*0.5, tilt)
			o := math.Max(0.1, 1-t/6.4) * 0.8
			rl := roleSpark
			if arm == 1 {
				rl = roleInk
			}
			g.put(i, j, o, rl, true)
			if r() < dust {
				w := 1 + t*0.9
				di := round((r() - 0.5) * 2 * w)
				dj := round((r() - 0.5) * 1.2 * w)
				g.put(i+di, j+dj, o*0.5, rl, true)
			}
			if arm == 0 && t >= 5.57 {
				ei, ej = i, j
			}
		}
	}
	return ei, ej
}

func (g *grid) spikes(length, diag int) {
	for d := 3; d <= length; d++ {
		o := math.Max(0.07, math.Pow(1-float64(d)/float64(length+1), 1.2))
		for _, a := range axes {
			g.put(a[0]*d, a[1]*d, o, roleSpark, false)
		}
	}
	for d := 3; d <= diag; d++ {
		o := 0.45 * math.Pow(1-float64(d)/float64(diag+1), 1.4)
		for _, a := range diagonals {
			g.put(a[0]*d, a[1]*d, o, roleSpark, false)
		}
	}
}

func (g *grid) rays(n int, r0, r1 float64, seed uint32, o float64) {
	r := rng(seed)
	for k := 0; k < n; k++ {
		a := r() * 2 * math.Pi
		a0 := r0 + r()*8
		a1 := a0 + 8 + r()*(r1-r0-8)
		rl := roleInk
		if r() < 0.55 {
			rl = roleWarm
		}
		for d := a0; d <= a1; d += 0.6 {
			if r() < 0.2 {
				continue
			}
			g.put(round(d*math.Cos(a)), round(d*math.Sin(a)), o*(1-(d-r0)/(r1-r0+6)), rl, true)
		}
	}
}

// shellArc draws a lumpy elliptical shell with gaps, lit from one side.
func (g *grid) shellArc(rr, ratio, phase, base float64, rl role, seed uint32, gaps float64, thick int) {
	r := rng(seed)
	seen := map[string]bool{}
	for t := 0; t < 9000; t++ {
		th := float64(t) / 9000 * 2 * math.Pi
		rj := rr * (1 + 0.045*math.Sin(3*th+phase) + 0.025*math.Sin(7*th+phase*2))
		if math.Sin(th*gaps+phase) > 0.4 {
			continue
		}
		lit := 0.3 + 0.7*(0.5+0.5*math.Cos(th-0.9))
		for w := 0; w < thick; w++ {
			i, j := rotate((rj+float64(w))*math.Cos(th), (rj+float64(w))*math.Sin(th)*ratio, tilt)
			k := fmt.Sprintf("%d,%d", i, j)
			if seen[k] {
				continue
			}
			seen[k] = true
			if r() < 0.12 {
				continue
			}
			g.put(i, j, math.Min(0.9, base*lit*(0.7+r()*0.3)), rl, true)
		}
	}
}

func (g *grid) dust(n int, seed uint32, spread float64) {
	r := rng(seed)
	for k := 0; k < n; k++ {
		th := r() * 2 * math.Pi
		rad := math.Pow(r(), 0.8) * spread
		i, j := rotate(rad*math.Cos(th), rad*math.Sin(th)*0.45, tilt)
		o := 0.08 + r()*0.22
		rl := roleSpark
		if r() < 0.5 {
			rl = roleInk
		}
		g.put(i, j, o, rl, true)
	}
}

func buildFull() *grid {
	g := newGrid(48)
	ei, ej := g.spiral(11, 1.05, 0.3)
	g.dust(380, 21, 38)
	g.shellArc(43, 0.9, 0.5, 1.0, roleWarm, 3, 4, 2)
	g.shellArc(35, 0.9, 2.1, 0.8, roleInk, 5, 3, 1)
	g.shellArc(27, 0.9, 3.4, 0.6, roleSpark, 7, 5, 1)
	g.rays(22, 16, 40, 9, 0.55)
	g.spikes(48, 22)
	g.halo(7, 0.3, roleSpark)
	g.starFull(0, 0, 22, roleSpark, roleCore, -1)
	g.starFull(ei, ej, 8, roleCore, roleCore, 3)
	return g
}

// ---------- output ----------

type palette struct {
	fill  [numRoles]string
	tile  string // empty for a transparent background
	cells string // fill for non-core cells drawn on a tile
}

func render(g *grid, p palette) string {
	span := float64(g.half)*unit + 3.5 // half-width of the artwork, with a margin
	c := span + 4.5                    // canvas centre
	size := 2 * c
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="512" height="512">`+"\n", size, size)
	b.WriteString("<title>Nova</title>\n")
	if p.tile != "" {
		// The artwork shrinks so the companion clears the rounded corner.
		const tileScale = 0.74
		fmt.Fprintf(&b, `<rect width="%.0f" height="%.0f" rx="%.0f" fill="%s"/>`+"\n", size, size, size*0.245, p.tile)
		fmt.Fprintf(&b, `<g transform="translate(%.1f %.1f) scale(%.2f) translate(%.1f %.1f)">`+"\n", c, c, tileScale, -c, -c)
	}
	for rl := role(0); rl < numRoles; rl++ {
		fill := p.fill[rl]
		if p.tile != "" && rl != roleCore {
			fill = p.cells
		}
		var rects strings.Builder
		for _, k := range g.order {
			cl := g.m[k]
			if cl.r != rl {
				continue
			}
			x := c + float64(cl.i)*unit - 2.375
			y := c + float64(cl.j)*unit - 2.375
			fmt.Fprintf(&rects, `<rect x="%.3g" y="%.3g" width="4.75" height="4.75" opacity="%.2f"/>`+"\n", x, y, cl.o)
		}
		if rects.Len() > 0 {
			fmt.Fprintf(&b, `<g fill="%s">`+"\n%s</g>\n", fill, rects.String())
		}
	}
	if p.tile != "" {
		b.WriteString("</g>\n")
	}
	b.WriteString("</svg>\n")
	return b.String()
}

func main() {
	out := flag.String("out", "assets/logo", "output directory")
	goFile := flag.String("go", "", "write the full mark's cells as a Go file at this path")
	favicon := flag.Bool("favicon", false, "write the favicon SVG and PNGs")
	flag.Parse()

	if *favicon {
		if err := writeFavicons(*out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("favicon: svg plus PNGs at 16, 32, 180, 512 in %s\n", *out)
		return
	}

	full, min := buildFull(), buildMin()
	if *goFile != "" {
		if err := writeGo(*goFile, full); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%d cells)\n", *goFile, len(full.order))
		return
	}

	dark := palette{fill: [numRoles]string{"#62c6ff", "#f2f6fb", "#fff4cf", "#ff9a5c"}}
	light := palette{fill: [numRoles]string{"#0b64c0", "#101722", "#f0a000", "#d4501a"}}
	mono := palette{fill: [numRoles]string{"#101722", "#101722", "#101722", "#101722"}}
	icon := palette{fill: [numRoles]string{"", "", "#ffd25a", ""}, tile: "#0b64c0", cells: "#f4f6f9"}
	files := []struct {
		name string
		g    *grid
		p    palette
	}{
		{"nova-mark-dark.svg", full, dark},
		{"nova-mark-light.svg", full, light},
		{"nova-mark-mono.svg", full, mono},
		{"nova-mark-min-dark.svg", min, dark},
		{"nova-mark-min-light.svg", min, light},
		{"nova-mark-min-mono.svg", min, mono},
		{"nova-icon.svg", min, icon},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(*out, f.name), []byte(render(f.g, f.p)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("full: %d cells, min: %d cells, %d files in %s\n", len(full.order), len(min.order), len(files), *out)
}

// writeGo emits the full mark's cells as a Go file for the TUI splash.
func writeGo(path string, g *grid) error {
	var b strings.Builder
	b.WriteString("// Code generated by assets/logo/gen -go; DO NOT EDIT.\n\npackage splash\n\n")
	fmt.Fprintf(&b, "// markHalf is the grid half-width: cells run from -markHalf to +markHalf.\nconst markHalf = %d\n\n", g.half)
	b.WriteString("// markCell is one cell of the full mark: grid position, opacity 0..255, and palette role.\ntype markCell struct {\n\tI, J int8\n\tO    uint8\n\tRole uint8\n}\n\n")
	fmt.Fprintf(&b, "// markCells is the full mark in draw order (%d cells).\nvar markCells = []markCell{\n", len(g.order))
	for _, k := range g.order {
		c := g.m[k]
		fmt.Fprintf(&b, "\t{I: %d, J: %d, O: %d, Role: %d},\n", c.i, c.j, int(math.Round(c.o*255)), int(c.r))
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("format generated file: %w", err)
	}
	return os.WriteFile(path, src, 0o644)
}

// ---------- favicon ----------

// buildFavicon is the 16 px mark: a short-armed star plus one warm companion
// cell, simplified so it still reads as a nova at favicon sizes.
func buildFavicon() *grid {
	g := newGrid(7)
	g.starMin(0, 0, 6, roleSpark, roleCore)
	g.put(6, 4, 1, roleWarm, false)
	return g
}

// faviconColors are the four roles at full opacity, the tile colour, and the
// PNG background for the large sizes.
var faviconColors = map[role]color.NRGBA{
	roleSpark: {R: 0x62, G: 0xc6, B: 0xff, A: 0xff},
	roleInk:   {R: 0xf2, G: 0xf6, B: 0xfb, A: 0xff},
	roleCore:  {R: 0xff, G: 0xf4, B: 0xcf, A: 0xff},
	roleWarm:  {R: 0xff, G: 0x9a, B: 0x5c, A: 0xff},
}

var faviconTile = color.NRGBA{R: 0x0b, G: 0x64, B: 0xc0, A: 0xff}

// writeFavicons writes the favicon SVG and the four PNG sizes.
func writeFavicons(outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("favicon: %w", err)
	}
	g := buildFavicon()
	svg := render(g, palette{fill: [numRoles]string{"#62c6ff", "#f2f6fb", "#fff4cf", "#ff9a5c"}})
	if err := os.WriteFile(filepath.Join(outDir, "nova-favicon.svg"), []byte(svg), 0o644); err != nil {
		return fmt.Errorf("favicon svg: %w", err)
	}
	for _, size := range []int{16, 32, 180, 512} {
		img := rasterize(g, size, size >= 180)
		path := filepath.Join(outDir, fmt.Sprintf("nova-favicon-%d.png", size))
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("favicon png %d: %w", size, err)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			return fmt.Errorf("favicon png %d: %w", size, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("favicon png %d: %w", size, err)
		}
	}
	return nil
}

// rasterize samples the grid with nearest neighbour, one output pixel per grid
// cell, so there is no anti-aliasing. Large sizes sit on the tile colour.
func rasterize(g *grid, size int, tile bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	span := g.half*2 + 1
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := x*span/size - g.half
			j := y*span/size - g.half
			c, ok := g.m[fmt.Sprintf("%d,%d", i, j)]
			var col color.NRGBA
			switch {
			case ok:
				col = faviconColors[c.r]
			case tile:
				col = faviconTile
			}
			img.SetNRGBA(x, y, col)
		}
	}
	return img
}
