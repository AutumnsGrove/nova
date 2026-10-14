// Package splash draws the Nova opening sequence on the character grid. The
// wordmark is composed from two small letter bitmaps; every font pixel becomes
// two terminal columns and one row, so a pixel stays square in a 1:2 cell.
package splash

// bold is the six-pixel-tall face used by the Full tier.
var bold = map[rune][]string{
	'n': {"#####.", "##..##", "##..##", "##..##", "##..##", "##..##"},
	'o': {".####.", "##..##", "##..##", "##..##", "##..##", ".####."},
	'v': {"##..##", "##..##", "##..##", ".####.", ".####.", "..##.."},
	'a': {".####.", "....##", ".#####", "##..##", "##..##", ".#####"},
}

// thin is the five-pixel-tall face used by the Compact and Thin tiers.
var thin = map[rune][]string{
	'n': {"####.", "#...#", "#...#", "#...#", "#...#"},
	'o': {".###.", "#...#", "#...#", "#...#", ".###."},
	'v': {"#...#", "#...#", "#...#", ".#.#.", "..#.."},
	'a': {".###.", "....#", ".####", "#...#", ".####"},
}

// point is one wordmark cell, relative to the mark's top-left.
type point struct{ x, y int }

// wordmark is a text run expanded to slash cells. Each font pixel becomes two
// adjacent columns, so stacked slashes join into a single diagonal.
type wordmark struct {
	w, h int
	pts  []point
	set  map[point]bool
}

// compose lays out text from a font and expands each pixel to two columns.
// gap is the number of font pixels between letters.
func compose(font map[rune][]string, text string, gap int) wordmark {
	var pixels []point
	ox, h := 0, 0
	for _, ch := range text {
		g, ok := font[ch]
		if !ok || len(g) == 0 {
			continue
		}
		for y, row := range g {
			for x, c := range row {
				if c == '#' {
					pixels = append(pixels, point{ox + x, y})
				}
			}
		}
		ox += len(g[0]) + gap
		h = len(g)
	}
	w := ox - gap
	pts := make([]point, 0, len(pixels)*2)
	set := make(map[point]bool, len(pixels)*2)
	for _, p := range pixels {
		for dx := 0; dx < 2; dx++ {
			q := point{p.x*2 + dx, p.y}
			pts = append(pts, q)
			set[q] = true
		}
	}
	return wordmark{w: w * 2, h: h, pts: pts, set: set}
}

// boldWord and thinWord are the two composed wordmarks. They are package
// values so a render never recomposes them.
var (
	boldWord = compose(bold, "nova", 1)
	thinWord = compose(thin, "nova", 1)
)
