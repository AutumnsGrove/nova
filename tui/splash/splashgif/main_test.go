//go:build !notui

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/AutumnsGrove/nova/tui/splash"
	"github.com/AutumnsGrove/nova/tui/styles"
)

const committed = "../../../assets/splash/nova-splash.gif"

func bg() color.RGBA { return parseHex(styles.Dark().Colors["bg"]) }

func truecolor() styles.Styles { return styles.Build(styles.Dark(), colorprofile.TrueColor) }

// decodeCommitted decodes the committed GIF and composites every frame onto a
// full canvas, as a player would with disposal "do not dispose".
func decodeCommitted(t *testing.T) (*gif.GIF, []*image.RGBA) {
	t.Helper()
	data, err := os.ReadFile(committed)
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return g, composite(g.Image, g.Config.Width, g.Config.Height)
}

// composite replays frames with no disposal onto full-size RGBA copies.
func composite(frames []*image.Paletted, w, h int) []*image.RGBA {
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	out := make([]*image.RGBA, len(frames))
	for i, f := range frames {
		draw.Draw(canvas, f.Bounds(), f, f.Bounds().Min, draw.Src)
		cp := image.NewRGBA(canvas.Bounds())
		copy(cp.Pix, canvas.Pix)
		out[i] = cp
	}
	return out
}

func TestSplashgif_SQN42_GIFIsValid(t *testing.T) {
	g, _ := decodeCommitted(t)
	if g.Config.Width != 672 || g.Config.Height != 294 {
		t.Fatalf("size = %dx%d", g.Config.Width, g.Config.Height)
	}
	if len(g.Image) != 150 {
		t.Fatalf("frames = %d, want 150", len(g.Image))
	}
	if g.LoopCount != 0 {
		t.Fatalf("loop count = %d, want 0 (forever)", g.LoopCount)
	}
}

func TestSplashgif_SQN43_GIFTiming(t *testing.T) {
	g, _ := decodeCommitted(t)
	if len(g.Delay) != 150 {
		t.Fatalf("delays = %d", len(g.Delay))
	}
	total := 0
	for i, d := range g.Delay {
		total += d
		if i == len(g.Delay)-1 {
			if d != 158 {
				t.Fatalf("last delay = %d, want 158", d)
			}
			continue
		}
		if d != 8 {
			t.Fatalf("frame %d delay = %d, want 8", i, d)
		}
	}
	if total != 1350 {
		t.Fatalf("total delay = %d, want 1350", total)
	}
}

func TestSplashgif_SQN44_GIFContent(t *testing.T) {
	_, frames := decodeCommitted(t)
	word := image.Rect(4*7, 7*14, 58*7, 13*14)
	star, drawn := nonBG(frames[0], word), nonBG(frames[37], word)
	if drawn < star*3 {
		t.Fatalf("frame 0 has %d word-region pixels and frame 37 only %d; no hatch", star, drawn)
	}
	starRow := image.Rect(76*7-30*7, 10*14, 76*7+30*7, 10*14+14)
	if n := nonBG(frames[37], starRow); n < 10 {
		t.Fatalf("frame 37 has %d spike pixels", n)
	}
	cells, _, _ := splash.Cells(cols, rows, truecolor(), colorprofile.TrueColor, "", 149*step)
	if !strings.Contains(flat(cells), "terminal coding agent") {
		t.Fatal("frame 149 is not the held scene")
	}
}

// nonBG counts pixels in r that are not the background.
func nonBG(img *image.RGBA, r image.Rectangle) int {
	n := 0
	b := bg()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.RGBAAt(x, y) != b {
				n++
			}
		}
	}
	return n
}

func flat(cells [][]splash.Cell) string {
	var b strings.Builder
	for _, row := range cells {
		for _, c := range row {
			b.WriteRune(c.Ch)
		}
		b.WriteRune('\n')
	}
	return b.String()
}

func TestSplashgif_SQN45_PaletteAndBackground(t *testing.T) {
	g, frames := decodeCommitted(t)
	if pal, ok := g.Config.ColorModel.(color.Palette); ok && len(pal) > 256 {
		t.Fatalf("global palette has %d colours", len(pal))
	}
	for i, f := range g.Image {
		if len(f.Palette) > 256 {
			t.Fatalf("frame %d palette has %d colours", i, len(f.Palette))
		}
		if frames[i].RGBAAt(0, 0) != bg() {
			t.Fatalf("frame %d corner is not the background", i)
		}
	}
}

func TestSplashgif_SQN46_Size(t *testing.T) {
	info, err := os.Stat(committed)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 2500*1024 {
		t.Fatalf("GIF is %d bytes, want at most 2.5 MB", info.Size())
	}
}

func TestSplashgif_SQN47_Deterministic(t *testing.T) {
	a, err := build()
	if err != nil {
		t.Fatal(err)
	}
	b, err := build()
	if err != nil {
		t.Fatal(err)
	}
	ab, err := encodeBytes(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := encodeBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ab, bb) {
		t.Fatal("the generator is not deterministic")
	}
}

func TestSplashgif_SQN48_CommittedGIFIsCurrent(t *testing.T) {
	want, err := build()
	if err != nil {
		t.Fatal(err)
	}
	wantFrames := composite(want.Image, want.Config.Width, want.Config.Height)
	_, got := decodeCommitted(t)
	if len(got) != len(wantFrames) {
		t.Fatalf("frame count = %d, want %d", len(got), len(wantFrames))
	}
	for i := range got {
		if !bytes.Equal(got[i].Pix, wantFrames[i].Pix) {
			t.Fatalf("frame %d differs from a fresh render", i)
		}
	}
}

func TestSplashgif_SQN49_READMEUsesGIF(t *testing.T) {
	data, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	head := text
	if i := strings.Index(text, "\n# "); i >= 0 {
		head = text[:i]
	}
	if !strings.Contains(head, `assets/splash/nova-splash.gif`) {
		t.Fatal("README head does not use the splash GIF")
	}
	if !strings.Contains(head, `width="672"`) {
		t.Fatal("README GIF has no width")
	}
	if strings.Contains(head, "<picture>") {
		t.Fatal("README still has the old picture block")
	}
	img := head[strings.Index(head, "<img"):]
	if alt := attr(img, "alt"); strings.TrimSpace(alt) == "" {
		t.Fatal("README GIF has empty alt text")
	}
	if _, err := os.Stat(committed); err != nil {
		t.Fatal(err)
	}
}

// attr reads one double-quoted HTML attribute from a tag.
func attr(tag, name string) string {
	i := strings.Index(tag, name+`="`)
	if i < 0 {
		return ""
	}
	rest := tag[i+len(name)+2:]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func TestSplashgif_SQN50_EveryGlyphCanBeDrawn(t *testing.T) {
	seen := map[rune]bool{}
	for _, size := range [][2]int{{96, 21}, {64, 13}, {46, 10}, {40, 5}, {30, 8}} {
		for _, frac := range []float64{0, 0.12, 0.22, 0.34, 0.5, 1, 5} {
			cells, _, ok := splash.Cells(size[0], size[1], truecolor(), colorprofile.TrueColor, "", time.Duration(frac*float64(splash.Sequence)))
			if !ok {
				t.Fatalf("%v not ok", size)
			}
			for _, row := range cells {
				for _, c := range row {
					if c.Ch != ' ' {
						seen[c.Ch] = true
					}
				}
			}
		}
	}
	for r := range seen {
		if !hasGlyph(r) {
			t.Fatalf("no shape or font entry for %q", r)
		}
	}
}

func TestSplashgif_SQN51_SlashesJoin(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, cellW, 2*cellH))
	col := color.RGBA{255, 255, 255, 255}
	drawGlyph(img, 0, 0, cellW, cellH, '╱', col)
	drawGlyph(img, 0, cellH, cellW, cellH, '╱', col)
	// Each diagonal runs corner to corner.
	if !opaque(img, cellW-1, 0) || !opaque(img, 0, cellH-1) {
		t.Fatal("upper diagonal does not run corner to corner")
	}
	if !opaque(img, cellW-1, cellH) || !opaque(img, 0, 2*cellH-1) {
		t.Fatal("lower diagonal does not run corner to corner")
	}
	// At the seam the two diagonals are within one cell's width, so a vertical
	// run reads as one continuous hatch rather than a gap.
	upperX := seamPixel(img, cellH-1, 0, cellW)
	lowerX := seamPixel(img, cellH, 0, cellW)
	if upperX < 0 || lowerX < 0 || abs(upperX-lowerX) >= cellW {
		t.Fatalf("slash hatch gap at the seam: upper %d lower %d", upperX, lowerX)
	}
}

// seamPixel finds the drawn pixel on a row.
func seamPixel(img *image.RGBA, y, x0, x1 int) int {
	for x := x0; x < x1; x++ {
		if opaque(img, x, y) {
			return x
		}
	}
	return -1
}

func opaque(img *image.RGBA, x, y int) bool {
	r, _, _, a := img.RGBAAt(x, y).RGBA()
	return a > 0 && r > 0
}

func TestSplashgif_SQN52_CommandWorks(t *testing.T) {
	var out, errOut bytes.Buffer
	path := filepath.Join(t.TempDir(), "s.gif")
	if err := run([]string{"-o", path}, &out, &errOut); err != nil {
		t.Fatalf("run: %v\n%s", err, errOut.String())
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := gif.DecodeAll(f); err != nil {
		t.Fatalf("decode: %v", err)
	}

	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-o", filepath.Join(blocker, "x.gif")}, &out, &errOut); err == nil {
		t.Fatal("bad output path did not fail")
	}
}
