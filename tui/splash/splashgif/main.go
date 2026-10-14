//go:build !notui

// Command splashgif renders the Nova splash sequence to an animated GIF for the
// README. It calls splash.Cells, the same code the terminal runs, so the picture
// on GitHub cannot drift from the product.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/tui/splash"
	"github.com/AutumnsGrove/nova/tui/styles"
)

const (
	cols, rows = 96, 21 // the Full tier
	cellW      = 7      // pixels per cell
	cellH      = 14     // pixels per cell
	frameCount = 150    // 100 sequence frames, 50 held
	step       = 80 * time.Millisecond
	frameDelay = 8       // centiseconds
	holdDelay  = 8 + 150 // centiseconds for the last frame
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "splashgif:", err)
		os.Exit(1)
	}
}

// run writes the GIF. A bad path is an error, not a panic.
func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("splashgif", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", filepath.Join("assets", "splash", "nova-splash.gif"), "output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	g, err := build()
	if err != nil {
		return err
	}
	if dir := filepath.Dir(*out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	data, err := encodeBytes(g)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Fprintf(stdout, "wrote %s (%d bytes)\n", *out, len(data))
	return nil
}

// encodeBytes serialises a GIF to memory.
func encodeBytes(g *gif.GIF) ([]byte, error) {
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// build renders every frame with the truecolor Full tier and assembles the GIF.
func build() (*gif.GIF, error) {
	st := styles.Build(styles.Dark(), colorprofile.TrueColor)
	bg := parseHex(styles.Dark().Colors["bg"])
	frames := make([]*image.RGBA, frameCount)
	for i := range frames {
		cells, tier, ok := splash.Cells(cols, rows, st, colorprofile.TrueColor, "", time.Duration(i)*step)
		if !ok || tier != splash.Full {
			return nil, fmt.Errorf("frame %d is not the Full tier", i)
		}
		frames[i] = raster(cells, bg)
	}
	pal := buildPalette(frames, bg)
	paletted := make([]*image.Paletted, len(frames))
	for i, f := range frames {
		paletted[i] = toPaletted(f, pal)
	}
	images := make([]*image.Paletted, len(paletted))
	delays := make([]int, len(paletted))
	disposal := make([]byte, len(paletted))
	for i, p := range paletted {
		if i == 0 {
			images[i] = p
		} else {
			images[i] = cropToChange(paletted[i-1], p)
		}
		delays[i] = frameDelay
		disposal[i] = gif.DisposalNone
	}
	delays[len(delays)-1] = holdDelay
	return &gif.GIF{
		Image:     images,
		Delay:     delays,
		Disposal:  disposal,
		LoopCount: 0, // forever
		Config:    image.Config{ColorModel: pal, Width: cols * cellW, Height: rows * cellH},
	}, nil
}

// raster draws a cell grid into an RGBA image at seven by fourteen pixels per
// cell, on the theme background.
func raster(cells [][]splash.Cell, bg color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, len(cells[0])*cellW, len(cells)*cellH))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	for y, row := range cells {
		for x, cell := range row {
			if cell.FG == nil || cell.Ch == ' ' {
				continue
			}
			r, g, b, _ := cell.FG.RGBA()
			drawGlyph(img, x*cellW, y*cellH, cellW, cellH, cell.Ch, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255})
		}
	}
	return img
}

// toPaletted maps an image to the shared palette.
func toPaletted(img *image.RGBA, pal color.Palette) *image.Paletted {
	out := image.NewPaletted(img.Bounds(), pal)
	cache := map[color.RGBA]uint8{}
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			c := img.RGBAAt(x, y)
			idx, ok := cache[c]
			if !ok {
				idx = uint8(nearest(pal, c))
				cache[c] = idx
			}
			out.SetColorIndex(x, y, idx)
		}
	}
	return out
}

// cropToChange returns the smallest sub-image of cur that differs from prev,
// or a one-pixel image when nothing changed. The palette slice is shared, so
// the encoder uses the global colour table.
func cropToChange(prev, cur *image.Paletted) *image.Paletted {
	b := cur.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	changed := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if prev.ColorIndexAt(x, y) == cur.ColorIndexAt(x, y) {
				continue
			}
			changed = true
			minX = min(minX, x)
			minY = min(minY, y)
			maxX = max(maxX, x+1)
			maxY = max(maxY, y+1)
		}
	}
	if !changed {
		return cur.SubImage(image.Rect(b.Min.X, b.Min.Y, b.Min.X+1, b.Min.Y+1)).(*image.Paletted)
	}
	return cur.SubImage(image.Rect(minX, minY, maxX, maxY)).(*image.Paletted)
}

// buildPalette makes a deterministic palette of at most 256 colours, with the
// background first so it is exact in every frame.
func buildPalette(imgs []*image.RGBA, bg color.RGBA) color.Palette {
	seen := map[color.RGBA]bool{}
	for _, img := range imgs {
		for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
			for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
				seen[img.RGBAAt(x, y)] = true
			}
		}
	}
	delete(seen, bg)
	colors := make([]color.RGBA, 0, len(seen))
	for c := range seen {
		colors = append(colors, c)
	}
	sortRGBA(colors)
	pal := color.Palette{bg}
	for _, c := range medianCut(colors, 255) {
		pal = append(pal, c)
	}
	return pal
}

// medianCut is a deterministic median-cut quantiser.
func medianCut(colors []color.RGBA, max int) []color.RGBA {
	if len(colors) <= max {
		return colors
	}
	boxes := [][]color.RGBA{colors}
	for len(boxes) < max {
		bi, best := -1, 0
		for i, b := range boxes {
			if _, d := spread(b); d > best {
				best, bi = d, i
			}
		}
		if bi < 0 || best == 0 {
			break
		}
		b := boxes[bi]
		ch, _ := spread(b)
		sortChannel(b, ch)
		mid := len(b) / 2
		boxes[bi] = b[:mid]
		boxes = append(boxes, b[mid:])
	}
	out := make([]color.RGBA, len(boxes))
	for i, b := range boxes {
		out[i] = average(b)
	}
	return out
}

// spread returns the box's widest channel and its spread.
func spread(b []color.RGBA) (int, int) {
	mn, mx := b[0], b[0]
	for _, c := range b[1:] {
		mn.R, mn.G, mn.B = min(mn.R, c.R), min(mn.G, c.G), min(mn.B, c.B)
		mx.R, mx.G, mx.B = max(mx.R, c.R), max(mx.G, c.G), max(mx.B, c.B)
	}
	dr, dg, db := int(mx.R-mn.R), int(mx.G-mn.G), int(mx.B-mn.B)
	if dg >= dr && dg >= db {
		return 1, dg
	}
	if db >= dr {
		return 2, db
	}
	return 0, dr
}

func sortChannel(b []color.RGBA, ch int) {
	sort.SliceStable(b, func(i, j int) bool {
		switch ch {
		case 1:
			if b[i].G != b[j].G {
				return b[i].G < b[j].G
			}
		case 2:
			if b[i].B != b[j].B {
				return b[i].B < b[j].B
			}
		default:
			if b[i].R != b[j].R {
				return b[i].R < b[j].R
			}
		}
		return rgbaLess(b[i], b[j])
	})
}

func sortRGBA(b []color.RGBA) {
	sort.Slice(b, func(i, j int) bool { return rgbaLess(b[i], b[j]) })
}

func rgbaLess(a, b color.RGBA) bool {
	if a.R != b.R {
		return a.R < b.R
	}
	if a.G != b.G {
		return a.G < b.G
	}
	return a.B < b.B
}

// average is the box's mean colour.
func average(b []color.RGBA) color.RGBA {
	if len(b) == 0 {
		return color.RGBA{A: 255}
	}
	var r, g, bl uint64
	for _, c := range b {
		r += uint64(c.R)
		g += uint64(c.G)
		bl += uint64(c.B)
	}
	n := uint64(len(b))
	return color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), 255}
}

// nearest is the palette index closest to c in RGB space.
func nearest(pal color.Palette, c color.RGBA) int {
	best, bestD := 0, math.MaxInt
	for i, p := range pal {
		r, g, b, _ := p.RGBA()
		dr, dg, db := int(c.R)-int(r>>8), int(c.G)-int(g>>8), int(c.B)-int(b>>8)
		d := dr*dr + dg*dg + db*db
		if d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// parseHex reads a theme's #rrggbb token.
func parseHex(s string) color.RGBA {
	if len(s) == 7 && s[0] == '#' {
		s = s[1:]
	}
	if len(s) != 6 {
		return color.RGBA{A: 255}
	}
	var v [3]uint8
	for i := 0; i < 3; i++ {
		v[i] = hexu8(s[i*2])<<4 | hexu8(s[i*2+1])
	}
	return color.RGBA{v[0], v[1], v[2], 255}
}

func hexu8(c byte) uint8 {
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
