//go:build !notui

//go:generate go run ../../assets/logo/gen -go mark_gen.go

// Package splash draws the Nova mark as terminal half-blocks, with a plain
// ASCII fallback for terminals without colour.
package splash

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// Level is how much of the full mark is drawn.
type Level int

const (
	// Full is one cell per half-block column (97 by 49).
	Full Level = iota
	// Half averages 2 by 2 cells (49 by 25).
	Half
	// Quarter averages 4 by 4 cells (25 by 13).
	Quarter
	// Plain is a small ASCII star for terminals without colour.
	Plain
)

// star is the plain fallback, seven rows.
var star = []string{
	"    *    ",
	"   ***   ",
	"  *****  ",
	" ******* ",
	"  *****  ",
	" **   ** ",
	"    *    ",
}

// cell is one downscaled mark cell.
type cell struct {
	opacity float64
	role    uint8
	present bool
}

// Render draws the mark and its caption. It picks the largest level that fits
// with a two-column and four-row margin, and reports ok false when even the
// plain star does not fit.
func Render(width, height int, st styles.Styles, p colorprofile.Profile, version string) (string, Level, bool) {
	if width < 24 || height < 14 {
		return "", Plain, false
	}
	if p < colorprofile.ANSI256 || st.Theme.Name == "mono" {
		return plainSplash(width, height, version), Plain, true
	}
	for _, lvl := range []Level{Full, Half, Quarter} {
		cols, cellRows := dims(lvl)
		if width >= cols+2 && height >= cellRows/2+1+4 {
			return drawMark(lvl, width, height, st, p, version), lvl, true
		}
	}
	return plainSplash(width, height, version), Plain, true
}

// dims returns the column and cell-row counts of a mark level.
func dims(l Level) (int, int) {
	switch l {
	case Half:
		return markHalf + 1, markHalf + 1
	case Quarter:
		return markHalf/2 + 1, markHalf/2 + 1
	default:
		return markHalf*2 + 1, markHalf*2 + 1
	}
}

// drawMark renders a cell-grid mark and its caption.
func drawMark(l Level, width, height int, st styles.Styles, p colorprofile.Profile, version string) string {
	cols, cellRows := dims(l)
	block := blockSize(l)
	grid := downscale(cols, cellRows, block)
	termRows := (cellRows + 1) / 2

	var lines []string
	for row := 0; row < termRows; row++ {
		var b strings.Builder
		for x := 0; x < cols; x++ {
			top := grid[x][row*2]
			var bottom cell
			if row*2+1 < cellRows {
				bottom = grid[x][row*2+1]
			}
			b.WriteString(pair(top, bottom, st, p))
		}
		lines = append(lines, b.String())
	}
	lines = append(lines, caption(width, version)...)
	return fit(lines, width, height)
}

// pair renders two stacked cells as one half-block character.
func pair(top, bottom cell, st styles.Styles, p colorprofile.Profile) string {
	switch {
	case top.present && bottom.present:
		return styled("█", top, st, p)
	case top.present:
		return styled("▀", top, st, p)
	case bottom.present:
		return styled("▄", bottom, st, p)
	default:
		return " "
	}
}

// styled renders one half-block in a cell's blended colour.
func styled(glyph string, c cell, st styles.Styles, p colorprofile.Profile) string {
	col, ok := blend(st, p, c.role, c.opacity)
	if !ok {
		return glyph
	}
	return lipgloss.NewStyle().Foreground(col).Render(glyph)
}

// downscale averages the mark into a cols by cellRows grid of blockSize
// squares.
func downscale(cols, cellRows, block int) [][]cell {
	grid := make([][]cell, cols)
	for x := range grid {
		grid[x] = make([]cell, cellRows)
	}
	for _, mc := range markCells {
		// Source cell coordinates, centred on the grid.
		sx, sy := int(mc.I)+markHalf, int(mc.J)+markHalf
		if sx < 0 || sy < 0 {
			continue
		}
		x, y := sx/block, sy/block
		if x >= cols || y >= cellRows {
			continue
		}
		g := &grid[x][y]
		if !g.present || float64(mc.O)/255 > g.opacity {
			g.role = mc.Role
		}
		g.opacity += float64(mc.O) / 255
		g.present = true
	}
	area := float64(block * block)
	for x := range grid {
		for y := range grid[x] {
			grid[x][y].opacity /= area
			if grid[x][y].opacity > 1 {
				grid[x][y].opacity = 1
			}
		}
	}
	return grid
}

// blockSize is the number of source cells per drawn cell.
func blockSize(l Level) int {
	switch l {
	case Half:
		return 2
	case Quarter:
		return 4
	default:
		return 1
	}
}

// roleKey maps a palette role to its token.
func roleKey(role uint8) string {
	switch role {
	case 1:
		return "ink"
	case 2:
		return "core"
	case 3:
		return "warm"
	default:
		return "spark"
	}
}

// blend mixes a role colour toward the background by opacity.
func blend(st styles.Styles, p colorprofile.Profile, role uint8, opacity float64) (color.Color, bool) {
	fg := st.Theme.Colors[roleKey(role)]
	bg := st.Theme.Colors["bg"]
	if fg == "" || bg == "" {
		return nil, false
	}
	r0, g0, b0 := parseHex(fg)
	rb, gb, bb := parseHex(bg)
	mix := func(a, b float64) uint8 { return uint8(a*opacity + b*(1-opacity)) }
	hex := "#" + hex2(mix(r0, rb)) + hex2(mix(g0, gb)) + hex2(mix(b0, bb))
	col := p.Convert(lipgloss.Color(hex))
	return col, col != nil
}

// plainSplash draws the ASCII star and caption.
func plainSplash(width, height int, version string) string {
	lines := append([]string{}, star...)
	lines = append(lines, caption(width, version)...)
	return fit(lines, width, height)
}

// caption is the empty line plus the three caption lines under the mark.
func caption(width int, version string) []string {
	label := "nova"
	if version != "" {
		label += " " + version
	}
	lines := []string{"", label, "press any key"}
	for i, l := range lines {
		lines[i] = centre(l, width)
	}
	return lines
}

// centre pads a line to the middle of width.
func centre(s string, width int) string {
	pad := (width - ansi.StringWidth(s)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

// fit truncates every line to width and the block to height.
func fit(lines []string, width, height int) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
	}
	return strings.Join(lines, "\n")
}

// parseHex parses #rgb or #rrggbb into 0..255 components.
func parseHex(s string) (float64, float64, float64) {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0
	}
	var r, g, b uint8
	for i := 0; i < 3; i++ {
		v := hexVal(s[i*2])<<4 | hexVal(s[i*2+1])
		switch i {
		case 0:
			r = v
		case 1:
			g = v
		case 2:
			b = v
		}
	}
	return float64(r), float64(g), float64(b)
}

// hexVal returns the value of one hex digit.
func hexVal(c byte) uint8 {
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

// hex2 formats a 0..255 component as two hex digits.
func hex2(v uint8) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[v>>4], digits[v&0xf]})
}
