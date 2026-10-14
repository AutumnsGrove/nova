//go:build !notui

package splash

import (
	"image/color"
	"math"
	"time"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// geom is one animated tier's fixed geometry, in cells relative to the scene.
type geom struct {
	w, h         int
	word         wordmark
	wordX, wordY int
	starX, starY int
	rmax, lh, lv int
	tagRow       int
	wordSpeed    float64
	wordDev      float64
}

var fullGeom = geom{
	w: 96, h: 21,
	word:  boldWord,
	wordX: 4, wordY: 7,
	starX: 76, starY: 10,
	rmax: 9, lh: 30, lv: 9,
	tagRow: 15, wordSpeed: 18, wordDev: 5,
}

var compactGeom = geom{
	w: 64, h: 13,
	word:  thinWord,
	wordX: 2, wordY: 4,
	starX: 54, starY: 6,
	rmax: 5, lh: 16, lv: 5,
	tagRow: 11, wordSpeed: 18, wordDev: 5,
}

// thinScene and lockupScene are the sizes of the two small tiers.
const (
	thinW, thinH     = 46, 10
	lockupW, lockupH = 40, 5
)

// drawScene draws one animated frame for a Full or Compact tier.
func drawScene(c *canvas, g geom, offX, offY int, version string, at time.Duration) {
	t := at.Seconds()
	u := clamp01(float64(at) / float64(Sequence))
	drawStars(c, t)
	drawOutburst(c, g, offX, offY, t, u)
	rev := smoothstep(0.05, 0.34, u)
	if rev > 0.02 {
		drawWord(c, g, offX, offY, t, rev, g.wordSpeed, g.wordDev, true)
	}
	drawTagline(c, g, offX, offY, t, u, version)
}

// drawStars fills the whole area with twinkling stars. The field keys on the
// absolute cell, so growing the area adds stars and never moves one.
func drawStars(c *canvas, t float64) {
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			if hash(x, y, 917) >= 0.055 {
				continue
			}
			glyph := starGlyph(hash(x, y, 101))
			speed := 0.6 + 1.6*hash(x, y, 202)
			phase := 2 * math.Pi * hash(x, y, 303)
			op := 0.1 + 0.4*(0.5+0.5*math.Sin(t*speed+phase))
			c.putRole(x, y, glyph, "ink", op)
		}
	}
}

// starGlyph picks one of four faint glyphs from a hash.
func starGlyph(h float64) rune {
	switch {
	case h < 0.35:
		return '·'
	case h < 0.7:
		return '·'
	case h < 0.86:
		return '.'
	default:
		return '+'
	}
}

// drawWord draws the shadow, then the shimmering slash face. rev is the
// left-to-right assembly progress (1 when complete).
func drawWord(c *canvas, g geom, offX, offY int, t, rev, speed, dev float64, assembly bool) {
	w := float64(g.word.w)
	for _, p := range g.word.pts {
		sx, sy := p.x+1, p.y+1
		if g.word.set[point{sx, sy}] {
			continue
		}
		if assembly && float64(sx)/w >= rev*1.25 {
			continue
		}
		op := 0.42
		if assembly {
			op *= rev
		}
		c.putRole(offX+g.wordX+sx, offY+g.wordY+sy, '╱', "warm", op)
	}
	for _, p := range g.word.pts {
		edge := float64(p.x)/w - rev*1.25
		if assembly && edge > 0 {
			continue
		}
		col, op := shimmerAt(p.x, p.y, g.word.w, t, speed, dev, 1.6, c.st)
		if assembly {
			lead := clamp01(1 + edge*10)
			col = mixColors(col, role(c.st, "core"), (1-lead)*0.8)
			op *= rev
		}
		c.put(offX+g.wordX+p.x, offY+g.wordY+p.y, '╱', col, op)
	}
}

// shimmerAt is the face colour and opacity at a word cell. A Gaussian
// highlight travels across the word; a rare sparkle adds a second peak.
func shimmerAt(x, y, w int, t, speed, dev, slant float64, st styles.Styles) (color.Color, float64) {
	span := float64(w + 36)
	p := math.Mod(t*speed, span) - 12
	d := float64(x) + slant*float64(y) - p
	hi := math.Exp(-(d * d) / (2 * dev * dev))
	tw := 0.0
	if hash(x/2, y, int(math.Floor(3*t))) > 0.985 {
		tw = 0.6
	}
	h := math.Max(hi, tw)
	across := clamp01(float64(x)/float64(w)) * 0.55
	base := mixColors(role(st, "spark"), role(st, "ink"), across)
	col := mixColors(base, role(st, "core"), h)
	flicker := 0.72 + 0.28*hash(x, y, int(math.Floor(2*t)))
	return col, (0.78 + 0.22*h) * flicker
}

// drawOutburst draws the shell, the spikes, the star, and the companion.
func drawOutburst(c *canvas, g geom, offX, offY int, t, u float64) {
	bu := u - 0.12
	f, radius, fade := 0.1, 0.0, 0.0
	if bu > 0 {
		if bu < 0.1 {
			f = 0.1 + 0.9*smoothstep(0, 1, bu/0.1)
		} else {
			f = 0.3 + 0.7*(1-smoothstep(0.1, 0.5, bu))
		}
		radius = smoothstep(0.06, 0.52, bu) * float64(g.rmax)
		fade = math.Max(1-smoothstep(0.36, 0.55, bu), 0.3)
	}
	if radius > 0.3 {
		outer := mixColors(role(c.st, "spark"), role(c.st, "warm"), clamp01(radius/float64(g.rmax)))
		for y := 0; y < g.h; y++ {
			for x := 0; x < g.w; x++ {
				dx := float64(x - g.starX)
				dy := float64(y - g.starY)
				d := math.Hypot(dx/2, dy)
				a := math.Atan2(dy, dx/2)
				clump := 0.55 + 0.45*math.Sin(a*3+1.7)*math.Sin(a*5-0.9+bu*20)
				i1 := math.Exp(-((d-radius)*(d-radius))/0.8) * clump * fade
				i2 := math.Exp(-((d-radius*0.6)*(d-radius*0.6))/1.1) *
					(0.5 + 0.5*math.Sin(a*2+bu*50)) * fade * 0.7
				if i1 >= i2 {
					c.put(offX+x, offY+y, dust(i1), outer, i1+0.15)
				} else {
					c.put(offX+x, offY+y, dust(i2), role(c.st, "spark"), i2+0.15)
				}
			}
		}
	}
	if bu > 0 {
		drawSpikes(c, g, offX, offY, f)
	}
	// The star waits quietly before ignition, then flares.
	glyph := '◆'
	col := mixColors(role(c.st, "spark"), role(c.st, "core"), f)
	if f > 0.5 {
		glyph = '✦'
	}
	if f > 0.8 {
		col = mixColors(role(c.st, "core"), role(c.st, "ink"), 0.5)
	}
	op := 0.45 + 0.25*(0.6+0.4*math.Sin(t*2.2))
	if bu > 0 {
		op = 0.6 + 0.4*f
	}
	c.put(offX+g.starX, offY+g.starY, glyph, col, op)
	o := t * 1.3
	c.putRole(offX+g.starX+int(math.Round(math.Cos(o)*4)), offY+g.starY+int(math.Round(math.Sin(o)*2)), '●', "warm", 0.9)
}

// drawSpikes draws the horizontal and vertical rays from the star.
func drawSpikes(c *canvas, g geom, offX, offY int, f float64) {
	lh := f * float64(g.lh)
	for k := 1; float64(k) <= lh; k++ {
		a := f * math.Pow(1-float64(k)/lh, 1.4)
		col := mixColors(role(c.st, "core"), role(c.st, "spark"), float64(k)/lh)
		c.put(offX+g.starX+k, offY+g.starY, '─', col, a)
		c.put(offX+g.starX-k, offY+g.starY, '─', col, a)
	}
	lv := f * float64(g.lv)
	for k := 1; float64(k) <= lv; k++ {
		a := f * math.Pow(1-float64(k)/lv, 1.2)
		col := mixColors(role(c.st, "core"), role(c.st, "spark"), float64(k)/lv)
		c.put(offX+g.starX, offY+g.starY+k, '│', col, a)
		c.put(offX+g.starX, offY+g.starY-k, '│', col, a)
	}
}

// drawTagline writes the caption under the word, fading in mid-sequence.
func drawTagline(c *canvas, g geom, offX, offY int, t, u float64, version string) {
	fade := smoothstep(0.42, 0.56, u)
	if fade <= 0 {
		return
	}
	x := offX + g.wordX
	row := offY + g.tagRow
	drawText(c, x, row, "terminal coding agent", "ink", 0.55*fade)
	if v := sanitize(version); v != "" {
		drawText(c, x+len([]rune("terminal coding agent"))+2, row, v, "ink", 0.35*fade)
	}
	drawText(c, x, row+1, "press any key", "spark", (0.4+0.2*math.Sin(t*2.4))*fade)
}

// drawText writes a run of glyphs in one role.
func drawText(c *canvas, x, y int, s, name string, opacity float64) {
	for i, r := range []rune(s) {
		c.putRole(x+i, y, r, name, opacity)
	}
}

// drawThin draws the thin word, its shadow, stars, and a centred prompt.
func drawThin(c *canvas, width, height int, at time.Duration) {
	t := at.Seconds()
	drawStars(c, t)
	offX, offY := (width-thinW)/2, (height-thinH)/2
	g := geom{word: thinWord, wordX: (thinW - thinWord.w) / 2, wordY: 2}
	drawWord(c, g, offX, offY, t, 1, 12, 4, false)
	drawText(c, offX+(thinW-13)/2, offY+8, "press any key", "spark", 0.45)
}

// drawLockup draws one centred line: three shimmering slashes and the name.
func drawLockup(c *canvas, width, height int, at time.Duration) {
	t := at.Seconds()
	drawStars(c, t)
	const text = "╱╱╱ nova"
	runes := []rune(text)
	x0, y0 := (width-len(runes))/2, (height-1)/2
	for i, r := range runes {
		if r == ' ' {
			continue
		}
		col, op := shimmerAt(i, 0, 9, t, 6, 1.6, 0, c.st)
		c.put(x0+i, y0, r, col, op)
	}
}

// dust maps a shell intensity to its glyph.
func dust(i float64) rune {
	switch {
	case i > 0.8:
		return '*'
	case i > 0.6:
		return '+'
	case i > 0.4:
		return ':'
	case i > 0.22:
		return '·'
	case i > 0.1:
		return '.'
	default:
		return ' '
	}
}

// smoothstep is the usual cubic ramp between a and b.
func smoothstep(a, b, x float64) float64 {
	if b == a {
		if x < a {
			return 0
		}
		return 1
	}
	t := clamp01((x - a) / (b - a))
	return t * t * (3 - 2*t)
}

// hash is a fixed, deterministic function with a uniform output in [0, 1).
func hash(x, y, z int) float64 {
	h := uint64(x)*0x9e3779b97f4a7c15 ^ uint64(y)*0xc2b2ae3d27d4eb4f ^ uint64(z)*0x165667b19e3779f9
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return float64(h>>11) / float64(uint64(1)<<53)
}
