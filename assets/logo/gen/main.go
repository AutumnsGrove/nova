// Command gen writes the Nova logo as SVG files.
//
// The mark is a cell grid: a four-point cell star at the centre, two spiral
// arms winding out from it, and a warm companion star at the end of one arm.
// Every cell is a square on a 49 by 49 grid, so the same artwork can later be
// rendered as terminal half-blocks for the TUI splash screen.
//
// Usage: go run ./assets/logo/gen [-out assets/logo]
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	unit  = 5.0 // cell pitch in canvas units
	half  = 24  // grid runs from -half to +half on both axes
	mid   = 128 // canvas centre
	tilt  = -0.6
	flat  = 0.5 // vertical squash of the spiral, for the tilted-disc look
	armTo = 5.6 // spiral parameter at the arm's end
)

type role int

const (
	roleSpark role = iota // primary star and the first arm
	roleInk               // second arm and its dust
	roleCore              // star centres and the companion
)

type cell struct {
	i, j int
	o    float64
	r    role
}

type grid struct {
	order []string
	m     map[string]cell
}

func newGrid() *grid { return &grid{m: map[string]cell{}} }

// put places a cell. A soft put never overwrites an existing cell.
func (g *grid) put(i, j int, o float64, r role, soft bool) {
	if abs(i) > half || abs(j) > half {
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

// star draws a four-point cell star of arm length l centred on (ci, cj).
func (g *grid) star(ci, cj, l int, arm, core role) {
	for d := 0; d <= l; d++ {
		o := 1.0
		if d > 1 {
			o = math.Max(0.05, math.Pow(1-float64(d-1)/float64(l), 1.25))
		}
		if d == 0 {
			g.put(ci, cj, o, core, false)
			continue
		}
		for _, p := range [][2]int{{d, 0}, {-d, 0}, {0, d}, {0, -d}} {
			g.put(ci+p[0], cj+p[1], o, arm, false)
		}
	}
	k := int(math.Floor(float64(l) / 2.4))
	for n := 1; n <= k; n++ {
		o := 0.62 * math.Pow(1-float64(n-1)/float64(k+1), 1.6)
		for _, p := range [][2]int{{n, n}, {-n, n}, {n, -n}, {-n, -n}} {
			g.put(ci+p[0], cj+p[1], o, arm, false)
		}
	}
}

// rotate rotates (x, y) by a and rounds half up, matching the design sheet.
func rotate(x, y, a float64) (int, int) {
	return int(math.Floor(x*math.Cos(a) - y*math.Sin(a) + 0.5)),
		int(math.Floor(x*math.Sin(a) + y*math.Cos(a) + 0.5))
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

// build returns the K3 mark.
func build() *grid {
	g := newGrid()
	r := rng(11)
	var endI, endJ int
	for arm := 0; arm < 2; arm++ {
		for t := 0.0; t <= armTo; t += 0.012 {
			rad := 3 + 3.9*t
			th := t*1.05 + float64(arm)*math.Pi
			i, j := rotate(rad*math.Cos(th), rad*math.Sin(th)*flat, tilt)
			o := math.Max(0.1, 1-t/6.2) * 0.75
			role := roleSpark
			if arm == 1 {
				role = roleInk
			}
			g.put(i, j, o, role, true)
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
	g.star(0, 0, 15, roleSpark, roleCore)
	g.star(endI, endJ, 4, roleCore, roleCore)
	return g
}

type palette struct {
	spark, ink, core string
	tile             string // empty for a transparent background
	cells            string // fill for cells drawn on a tile
}

func (p palette) fill(r role) string {
	switch r {
	case roleInk:
		return p.ink
	case roleCore:
		return p.core
	}
	return p.spark
}

func render(g *grid, p palette) string {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="22 22 212 212" width="512" height="512">` + "\n")
	b.WriteString(`<title>Nova</title>` + "\n")
	if p.tile != "" {
		fmt.Fprintf(&b, `<rect x="22" y="22" width="212" height="212" rx="52" fill="%s"/>`+"\n", p.tile)
	}
	if p.tile != "" {
		// Shrink the artwork so the companion clears the rounded corner.
		b.WriteString(`<g transform="translate(128 128) scale(0.74) translate(-128 -128)">` + "\n")
	}
	for _, k := range g.order {
		c := g.m[k]
		sz := 4.75
		fill := p.fill(c.r)
		if p.tile != "" {
			fill = p.cells
			if c.r == roleCore {
				fill = p.core
			}
		}
		x := float64(mid) + float64(c.i)*unit - sz/2
		y := float64(mid) + float64(c.j)*unit - sz/2
		fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s" opacity="%.2f"/>`+"\n", x, y, sz, sz, fill, c.o)
	}
	if p.tile != "" {
		b.WriteString("</g>\n")
	}
	b.WriteString("</svg>\n")
	return b.String()
}

func main() {
	out := flag.String("out", "assets/logo", "output directory")
	flag.Parse()
	g := build()
	files := map[string]palette{
		"nova-mark-dark.svg":  {spark: "#62c6ff", ink: "#f2f6fb", core: "#fff4cf"},
		"nova-mark-light.svg": {spark: "#0b64c0", ink: "#101722", core: "#f0a000"},
		"nova-mark-mono.svg":  {spark: "#101722", ink: "#101722", core: "#101722"},
		"nova-icon.svg":       {tile: "#0b64c0", cells: "#f4f6f9", core: "#ffd25a"},
	}
	for name, p := range files {
		if err := os.WriteFile(filepath.Join(*out, name), []byte(render(g, p)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("wrote %d cells to %d files in %s\n", len(g.order), len(files), *out)
}
