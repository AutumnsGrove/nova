//go:build !notui

// Package splash draws the Nova opening sequence: a slash wordmark that slides
// in beside an igniting star, over a field of twinkling stars. The scene is
// drawn on the character grid rather than downscaled from the logo mark.
package splash

import (
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// Tier is how much of the scene is drawn.
type Tier int

const (
	// Full is the side-by-side scene: 96 by 21 cells.
	Full Tier = iota
	// Compact is the thin wordmark and a smaller outburst: 64 by 13.
	Compact
	// Thin is the thin wordmark and stars, with no outburst: 46 by 10.
	Thin
	// Lockup is a one-line lockup and a few stars: 40 by 5.
	Lockup
	// Plain is text only, with no colour and no motion.
	Plain
)

// Sequence is the length of the opening sequence. After it the scene is held.
const Sequence = 8 * time.Second

// Animated reports whether a tier changes over time. The UI ticks only when it
// does.
func Animated(t Tier) bool { return t != Plain }

// Render draws the splash into a width by height area at the given time since
// the splash first appeared. It reports the tier used, and ok false when even
// the plain tier does not fit. Output is exactly height lines, none wider than
// width. It is pure: the same arguments give the same bytes.
func Render(width, height int, st styles.Styles, p colorprofile.Profile, version string, at time.Duration) (string, Tier, bool) {
	c, tier, ok := build(width, height, st, p, version, at)
	if !ok {
		return "", tier, false
	}
	return c.text(), tier, true
}

// Cells is Render before it is turned into text: a height by width grid. Render
// is Cells with each row serialised, so the two always agree.
func Cells(width, height int, st styles.Styles, p colorprofile.Profile, version string, at time.Duration) ([][]Cell, Tier, bool) {
	c, tier, ok := build(width, height, st, p, version, at)
	if !ok {
		return nil, tier, false
	}
	return c.cells, tier, true
}

// build chooses the largest tier that fits and draws it.
func build(width, height int, st styles.Styles, p colorprofile.Profile, version string, at time.Duration) (*canvas, Tier, bool) {
	if width < 20 || height < 3 {
		return nil, Plain, false
	}
	if p < colorprofile.ANSI256 || st.Theme.Name == "mono" {
		return plainCanvas(width, height, version), Plain, true
	}
	switch {
	case width >= fullGeom.w && height >= fullGeom.h:
		c := newCanvas(width, height, st, p)
		drawScene(c, fullGeom, (width-fullGeom.w)/2, (height-fullGeom.h)/2, version, at)
		return c, Full, true
	case width >= compactGeom.w && height >= compactGeom.h:
		c := newCanvas(width, height, st, p)
		drawScene(c, compactGeom, (width-compactGeom.w)/2, (height-compactGeom.h)/2, version, at)
		return c, Compact, true
	case width >= thinW && height >= thinH:
		c := newCanvas(width, height, st, p)
		drawThin(c, width, height, at)
		return c, Thin, true
	case width >= lockupW && height >= lockupH:
		c := newCanvas(width, height, st, p)
		drawLockup(c, width, height, at)
		return c, Lockup, true
	default:
		return plainCanvas(width, height, version), Plain, true
	}
}

// plainCanvas is the static, colourless fallback: the name and a prompt,
// centred, with no escape codes and no motion.
func plainCanvas(width, height int, version string) *canvas {
	c := newCanvas(width, height, styles.Styles{}, colorprofile.ASCII)
	label := "nova"
	if v := sanitize(version); v != "" {
		label += " " + v
	}
	top := (height - 2) / 2
	if top < 0 {
		top = 0
	}
	c.writePlain((width-len([]rune(label)))/2, top, label)
	c.writePlain((width-len("press any key"))/2, top+1, "press any key")
	return c
}
