//go:build !notui

package main

import (
	"image"
	"image/color"
	"math"
	"strings"
)

// font5x7 is a tiny lowercase font for the ASCII glyphs the splash emits:
// the tagline letters, a space, and the shell's punctuation. Each pattern has
// seven rows of five columns separated by '/'.
var font5x7 = map[rune]string{
	'a': ".###./....#/.####/#...#/#...#/#...#/.####",
	'c': ".###./#...#/#..../#..../#..../#...#/.###.",
	'd': "..###/....#/.####/#...#/#...#/#...#/.####",
	'e': ".###./#...#/#...#/#####/#..../#...#/.###.",
	'g': ".####/#...#/#...#/#...#/.####/....#/.###.",
	'i': ".###./..#../..#../..#../..#../..#../.###.",
	'k': "#...#/#..#./#.#../##.../#.#../#..#./#...#",
	'l': ".##../..#../..#../..#../..#../..#../.####",
	'm': "...../##.#./#.#.#/#.#.#/#.#.#/#.#.#/#.#.#",
	'n': "...../#.##./##..#/#...#/#...#/#...#/#...#",
	'o': "...../.###./#...#/#...#/#...#/#...#/.###.",
	'p': "...../####./#...#/#...#/#...#/####./#....",
	'r': "...../#.##./##..#/#..../#..../#..../#....",
	's': "...../.####/#..../.###./....#/#...#/.####",
	't': "..#../..#../#####/..#../..#../..#../..###",
	'v': "...../#...#/#...#/#...#/#...#/.#.#./..#..",
	'y': "...../#...#/#...#/#...#/.####/....#/.###.",
	'0': ".###./#...#/#..##/#.#.#/##..#/#...#/.###.",
	'1': "..#../.##../..#../..#../..#../..#../.###.",
	'2': ".###./#...#/....#/..##./.#.../#..../#####",
	'3': "#####/...#./..#../...#./....#/#...#/.###.",
	'4': "...#./..##./.#.#./#..#./#####/...#./...#.",
	'5': "#####/#..../####./....#/....#/#...#/.###.",
	'6': ".###./#...#/#..../####./#...#/#...#/.###.",
	'7': "#####/....#/...#./..#../.#.../.#.../.#...",
	'8': ".###./#...#/#...#/.###./#...#/#...#/.###.",
	'9': ".###./#...#/#...#/.####/....#/#...#/.###.",
	' ': "...../...../...../...../...../...../.....",
	'.': "...../...../...../...../...../.##../.##..",
	':': "...../.##../.##../...../.##../.##../.....",
	'+': "...../..#../..#../#####/..#../..#../.....",
	'*': "...../#.#.#/.###./#####/.###./#.#.#/.....",
}

// hasGlyph reports whether the rasteriser knows how to draw a rune. Every rune
// splash.Cells can emit must be covered.
func hasGlyph(r rune) bool {
	if _, ok := font5x7[r]; ok {
		return true
	}
	switch r {
	case '█', '╱', '─', '│', '·', '●', '◆', '✦':
		return true
	}
	return false
}

// drawGlyph paints one cell's glyph into the image at (x0, y0), cellW by cellH.
func drawGlyph(img *image.RGBA, x0, y0, cw, ch int, r rune, col color.RGBA) {
	px := func(x, y int) { set(img, x, y, col) }
	switch r {
	case '█':
		fillRect(img, x0, y0, x0+cw, y0+ch, col)
	case '╱':
		// A diagonal from the top-right corner to the bottom-left corner, so
		// a vertical run of slashes reads as a continuous hatch.
		for y := 0; y < ch; y++ {
			x := (cw - 1) - (y*(cw-1))/(ch-1)
			px(x0+x, y0+y)
		}
	case '─':
		fillRect(img, x0, y0+ch/2-1, x0+cw, y0+ch/2+1, col)
	case '│':
		fillRect(img, x0+cw/2-1, y0, x0+cw/2+1, y0+ch, col)
	case '·':
		disc(img, x0+cw/2, y0+ch/2, 1, col)
	case '●':
		disc(img, x0+cw/2, y0+ch/2, 2, col)
	case '◆':
		diamond(img, x0+cw/2, y0+ch/2, 3, col)
	case '✦':
		star(img, x0+cw/2, y0+ch/2, 3, col)
	default:
		drawASCII(img, x0, y0, cw, ch, r, col)
	}
}

// drawASCII scales a 5x7 glyph to fill the cell.
func drawASCII(img *image.RGBA, x0, y0, cw, ch int, r rune, col color.RGBA) {
	pat, ok := font5x7[r]
	if !ok {
		return
	}
	rows := strings.Split(pat, "/")
	for fy, row := range rows {
		for fx, c := range row {
			if c != '#' {
				continue
			}
			x1 := x0 + fx*cw/5
			x2 := x0 + (fx+1)*cw/5
			y1 := y0 + fy*ch/7
			y2 := y0 + (fy+1)*ch/7
			fillRect(img, x1, y1, x2, y2, col)
		}
	}
}

// set paints one pixel if it is inside the image.
func set(img *image.RGBA, x, y int, col color.RGBA) {
	if image.Pt(x, y).In(img.Rect) {
		img.SetRGBA(x, y, col)
	}
}

// fillRect fills a half-open rectangle cl:ipping to the image.
func fillRect(img *image.RGBA, x1, y1, x2, y2 int, col color.RGBA) {
	for y := y1; y < y2; y++ {
		for x := x1; x < x2; x++ {
			set(img, x, y, col)
		}
	}
}

// disc fills a circle of radius r.
func disc(img *image.RGBA, cx, cy, r int, col color.RGBA) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				set(img, cx+dx, cy+dy, col)
			}
		}
	}
}

// diamond fills a rhombus of radius r.
func diamond(img *image.RGBA, cx, cy, r int, col color.RGBA) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if abs(dx)+abs(dy) <= r {
				set(img, cx+dx, cy+dy, col)
			}
		}
	}
}

// star fills a four-point astroid; the cell correction is the caller's.
func star(img *image.RGBA, cx, cy, r int, col color.RGBA) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			nx := math.Abs(float64(dx)) / float64(r)
			ny := math.Abs(float64(dy)) / float64(r)
			if math.Sqrt(nx)+math.Sqrt(ny) <= 1.0 {
				set(img, cx+dx, cy+dy, col)
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
