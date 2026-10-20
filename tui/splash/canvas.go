//go:build !notui

package splash

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// Cell is one drawn terminal cell. Ch is a space for an empty cell; FG is the
// foreground already converted for the profile (nil means the default).
type Cell struct {
	Ch rune
	FG color.Color
}

// canvas is a height by width grid of cells being drawn. It resolves theme
// roles, blends them toward the background by an opacity, converts the result
// for the profile, and serialises runs that share a colour with one escape.
type canvas struct {
	w, h  int
	cells [][]Cell
	st    styles.Styles
	p     colorprofile.Profile
	bg    color.Color
}

// newCanvas returns an empty grid. It never emits a literal colour: every
// colour comes from a theme role.
func newCanvas(w, h int, st styles.Styles, p colorprofile.Profile) *canvas {
	c := &canvas{w: w, h: h, st: st, p: p, bg: role(st, "bg")}
	c.cells = make([][]Cell, h)
	for y := range c.cells {
		c.cells[y] = make([]Cell, w)
		for x := range c.cells[y] {
			c.cells[y][x] = Cell{Ch: ' '}
		}
	}
	return c
}

// put writes one glyph at (x, y). col is a resolved theme colour; the canvas
// blends it toward the background by opacity and converts it for the profile.
// Out-of-range, invisible, and blank cells are dropped.
func (c *canvas) put(x, y int, glyph rune, col color.Color, opacity float64) {
	if glyph == ' ' || col == nil {
		return
	}
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	if opacity < 0.03 {
		return
	}
	converted := c.p.Convert(mixColors(c.bg, col, clamp01(opacity)))
	if converted == nil {
		return
	}
	c.cells[y][x] = Cell{Ch: glyph, FG: converted}
}

// putRole is put for a single theme role.
func (c *canvas) putRole(x, y int, glyph rune, name string, opacity float64) {
	c.put(x, y, glyph, role(c.st, name), opacity)
}

// writePlain sets glyphs with no colour, for the Plain tier.
func (c *canvas) writePlain(x, y int, s string) {
	for i, r := range []rune(s) {
		px := x + i
		if px >= 0 && px < c.w && y >= 0 && y < c.h {
			c.cells[y][px] = Cell{Ch: r}
		}
	}
}

// text serialises the grid: one line per row, each exactly w cells wide.
// Adjacent cells that share a colour produce a single escape sequence.
func (c *canvas) text() string {
	lines := make([]string, c.h)
	for y := 0; y < c.h; y++ {
		var b strings.Builder
		current := ""
		for x := 0; x < c.w; x++ {
			cell := c.cells[y][x]
			attr := sgr(cell.FG)
			if attr != current {
				if current != "" {
					b.WriteString(ansi.ResetStyle)
				}
				if attr != "" {
					b.WriteString("\x1b[" + attr + "m")
				}
				current = attr
			}
			b.WriteRune(cell.Ch)
		}
		if current != "" {
			b.WriteString(ansi.ResetStyle)
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n")
}

// sgr is the SGR attribute for a colour, or "" for the default foreground.
func sgr(c color.Color) string {
	if c == nil {
		return ""
	}
	return strings.Join(ansi.Style(nil).ForegroundColor(c), ";")
}

// role resolves a theme token to its colour, or nil when the token is absent.
func role(st styles.Styles, name string) color.Color {
	hex := st.Theme.Colors[name]
	if hex == "" {
		return nil
	}
	return parseHexColor(hex)
}

// mixColors blends b over a by t: t 0 gives a, t 1 gives b. The canvas uses it
// to fade a glyph toward the background.
func mixColors(a, b color.Color, t float64) color.Color {
	ar, ag, ab := rgb8(a)
	br, bg, bb := rgb8(b)
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x)*(1-t) + float64(y)*t + 0.5)
	}
	return color.RGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: 255}
}

// rgb8 reduces a colour to opaque 8-bit components.
func rgb8(c color.Color) (uint8, uint8, uint8) {
	if c == nil {
		return 0, 0, 0
	}
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

// parseHexColor parses #rgb or #rrggbb.
func parseHexColor(s string) color.Color {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return nil
	}
	v := func(c byte) uint8 {
		switch {
		case c >= '0' && c <= '9':
			return c - '0'
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10
		default:
			return 0
		}
	}
	return color.RGBA{
		R: v(s[0])<<4 | v(s[1]),
		G: v(s[2])<<4 | v(s[3]),
		B: v(s[4])<<4 | v(s[5]),
		A: 255,
	}
}

// sanitize drops runes that are not exactly one column wide, so a version with
// a wide or control character can never widen a line or inject an escape.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if ansi.StringWidth(string(r)) == 1 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// clamp01 limits v to [0, 1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
