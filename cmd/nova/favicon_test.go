package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// runFaviconGen runs the logo generator's favicon mode into out.
func runFaviconGen(t *testing.T, out string) {
	t.Helper()
	cmd := exec.Command("go", "run", "./assets/logo/gen", "-favicon", "-out", out)
	cmd.Dir = filepath.Join("..", "..")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate favicon: %v\n%s", err, b)
	}
}

func readFavicon(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	return img
}

func TestFavicon_FAV01_SVGTransparent(t *testing.T) {
	dir := t.TempDir()
	runFaviconGen(t, dir)
	svg := string(readFavicon(t, dir, "nova-favicon.svg"))
	if !bytes.Contains([]byte(svg), []byte("<svg")) || !bytes.Contains([]byte(svg), []byte("xmlns=\"http://www.w3.org/2000/svg\"")) {
		t.Fatalf("not a valid SVG:\n%s", svg)
	}
	if bytes.Contains([]byte(svg), []byte("rx=")) {
		t.Fatalf("favicon SVG has a background tile, want transparency:\n%s", svg)
	}
}

func TestFavicon_FAV02_PNGSizes(t *testing.T) {
	dir := t.TempDir()
	runFaviconGen(t, dir)
	for _, size := range []int{16, 32, 180, 512} {
		img := decodePNG(t, readFavicon(t, dir, faviconName(size)))
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Fatalf("%d PNG is %dx%d", size, b.Dx(), b.Dy())
		}
	}
}

func faviconName(size int) string {
	return "nova-favicon-" + strconv.Itoa(size) + ".png"
}

func TestFavicon_FAV03_StarAndWarmCell(t *testing.T) {
	dir := t.TempDir()
	runFaviconGen(t, dir)
	img := decodePNG(t, readFavicon(t, dir, faviconName(16)))
	warm, bright := false, false
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			if r>>8 > 200 && g>>8 > 100 && g>>8 < 200 && bl>>8 < 150 {
				warm = true
			}
		}
	}
	r, g, bl, _ := img.At(8, 8).RGBA()
	if r>>8 > 230 && g>>8 > 230 && bl>>8 > 200 {
		bright = true
	}
	if !warm {
		t.Fatal("16 px favicon has no warm companion pixel")
	}
	if !bright {
		t.Fatalf("16 px favicon centre is not bright: %v", img.At(8, 8))
	}
}

func TestFavicon_FAV04_Deterministic(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	runFaviconGen(t, first)
	runFaviconGen(t, second)
	for _, name := range []string{"nova-favicon.svg", faviconName(16), faviconName(32), faviconName(180), faviconName(512)} {
		if !bytes.Equal(readFavicon(t, first, name), readFavicon(t, second, name)) {
			t.Fatalf("%s differs between runs", name)
		}
	}
}

func TestFavicon_FAV05_ArmsAtLeastTwoPixels(t *testing.T) {
	dir := t.TempDir()
	runFaviconGen(t, dir)
	img := decodePNG(t, readFavicon(t, dir, faviconName(16)))
	arm := 0
	for x := 9; x < 16; x++ {
		_, _, _, a := img.At(x, 8).RGBA()
		if a == 0 {
			break
		}
		arm++
	}
	if arm < 2 {
		t.Fatalf("star arm is %d pixels at 16 px, want at least 2", arm)
	}
}
