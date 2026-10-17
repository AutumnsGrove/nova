//go:build !notui

package layout

import (
	"math/rand"
	"testing"
)

func TestLayout_LYT01_Typical(t *testing.T) {
	r := Compute(Inputs{Width: 80, Height: 24, EditorLines: 1})
	if r.Header.H != 1 {
		t.Fatalf("header height = %d, want 1", r.Header.H)
	}
	if r.Footer.H != 1 {
		t.Fatalf("footer height = %d, want 1", r.Footer.H)
	}
	if r.Editor.H != 1 {
		t.Fatalf("editor height = %d, want 1", r.Editor.H)
	}
	// 24 - header(1) - footer(1) - editor(1) = 21.
	if r.Main.H != 21 {
		t.Fatalf("main height = %d, want 21", r.Main.H)
	}
	checkTiles(t, Inputs{Width: 80, Height: 24, EditorLines: 1})
}

func TestLayout_LYT02_NoOverlapAllInside(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 20000; i++ {
		in := Inputs{
			Width:       rng.Intn(300) + 1,
			Height:      rng.Intn(300) + 1,
			EditorLines: rng.Intn(25) - 2,
			AboveLines:  rng.Intn(20) - 2,
			BelowLines:  rng.Intn(20) - 2,
			WantSidebar: rng.Intn(2) == 0,
		}
		checkTiles(t, in)
	}
}

func TestLayout_LYT03_HeaderDroppedWhenShort(t *testing.T) {
	r := Compute(Inputs{Width: 80, Height: 10, EditorLines: 1})
	if r.Header.H != 0 {
		t.Fatalf("header height = %d, want 0 at height 10", r.Header.H)
	}
}

func TestLayout_LYT04_EditorGrowsToTen(t *testing.T) {
	r := Compute(Inputs{Width: 80, Height: 40, EditorLines: 20})
	if r.Editor.H != 10 {
		t.Fatalf("editor height = %d, want 10", r.Editor.H)
	}
}

func TestLayout_LYT05_MainKeepsThreeLines(t *testing.T) {
	r := Compute(Inputs{Width: 80, Height: 12, EditorLines: 8, AboveLines: 8, BelowLines: 8})
	if r.Main.H < 3 {
		t.Fatalf("main height = %d, want at least 3", r.Main.H)
	}
	checkTiles(t, Inputs{Width: 80, Height: 12, EditorLines: 8, AboveLines: 8, BelowLines: 8})
}

func TestLayout_LYT06_WidgetsClampToEight(t *testing.T) {
	r := Compute(Inputs{Width: 200, Height: 60, EditorLines: 1, AboveLines: 50, BelowLines: 50})
	if r.Above.H != 8 {
		t.Fatalf("above height = %d, want 8", r.Above.H)
	}
	if r.Below.H != 8 {
		t.Fatalf("below height = %d, want 8", r.Below.H)
	}
}

func TestLayout_LYT07_SidebarOnlyWhenWide(t *testing.T) {
	narrow := Compute(Inputs{Width: 119, Height: 24, EditorLines: 1, WantSidebar: true})
	if narrow.Sidebar.W != 0 {
		t.Fatalf("sidebar width = %d at width 119, want 0", narrow.Sidebar.W)
	}
	wide := Compute(Inputs{Width: 120, Height: 24, EditorLines: 1, WantSidebar: true})
	if wide.Sidebar.W != 32 {
		t.Fatalf("sidebar width = %d at width 120, want 32", wide.Sidebar.W)
	}
	if wide.Sidebar.H != wide.Main.H {
		t.Fatalf("sidebar height %d != main height %d", wide.Sidebar.H, wide.Main.H)
	}
	if wide.Main.W != 120-32 {
		t.Fatalf("main width = %d, want %d", wide.Main.W, 120-32)
	}
}

func TestLayout_LYT08_TinyTerminal(t *testing.T) {
	checkTiles(t, Inputs{Width: 10, Height: 5, EditorLines: 8, AboveLines: 8, BelowLines: 8, WantSidebar: true})
	r := Compute(Inputs{Width: 10, Height: 5, EditorLines: 8, AboveLines: 8, BelowLines: 8, WantSidebar: true})
	for name, rect := range map[string]Rect{"header": r.Header, "main": r.Main, "above": r.Above, "editor": r.Editor, "below": r.Below, "footer": r.Footer, "sidebar": r.Sidebar} {
		if rect.W < 0 || rect.H < 0 {
			t.Fatalf("%s has negative size: %+v", name, rect)
		}
	}
}

func TestLayout_LYT09_OrderOfDropping(t *testing.T) {
	in := Inputs{Width: 200, Height: 24, EditorLines: 1, AboveLines: 8, BelowLines: 8, WantSidebar: true}

	// At 24, everything is present.
	full := Compute(in)
	if full.Header.H != 1 || full.Above.H == 0 || full.Below.H == 0 || full.Sidebar.W == 0 {
		t.Fatalf("height 24 should show header, widgets, and sidebar: %+v", full)
	}

	// Widgets are sacrificed first: by height 12 they are smaller than at 24,
	// while the header and sidebar are still present.
	mid := Compute(Inputs{Width: in.Width, Height: 12, EditorLines: 1, AboveLines: 8, BelowLines: 8, WantSidebar: true})
	if mid.Header.H != 1 || mid.Sidebar.W == 0 {
		t.Fatalf("height 12 should still show header and sidebar: %+v", mid)
	}
	if mid.Above.H+mid.Below.H >= full.Above.H+full.Below.H {
		t.Fatalf("widgets should shrink before the header drops: mid=%d full=%d", mid.Above.H+mid.Below.H, full.Above.H+full.Below.H)
	}

	// The header goes at 11, but widgets and the sidebar survive to 8.
	short := Compute(Inputs{Width: in.Width, Height: 11, EditorLines: 1, AboveLines: 8, BelowLines: 8, WantSidebar: true})
	if short.Header.H != 0 {
		t.Fatalf("header should be dropped at height 11")
	}
	if short.Above.H == 0 && short.Below.H == 0 {
		t.Fatalf("widgets should survive at height 11")
	}
	if short.Sidebar.W == 0 {
		t.Fatalf("sidebar should survive at height 11")
	}

	// Under 8, widgets and the sidebar are dropped last.
	tiny := Compute(Inputs{Width: in.Width, Height: 7, EditorLines: 1, AboveLines: 8, BelowLines: 8, WantSidebar: true})
	if tiny.Above.H != 0 || tiny.Below.H != 0 {
		t.Fatalf("widgets should be dropped under 8 lines: %+v", tiny)
	}
	if tiny.Sidebar.W != 0 {
		t.Fatalf("sidebar should be dropped under 8 lines")
	}
}

func TestLayout_LYT10_Deterministic(t *testing.T) {
	in := Inputs{Width: 137, Height: 43, EditorLines: 5, AboveLines: 2, BelowLines: 3, WantSidebar: true}
	first := Compute(in)
	for i := 0; i < 100; i++ {
		if got := Compute(in); got != first {
			t.Fatalf("Compute is not deterministic: %+v != %+v", got, first)
		}
	}
}

// TestProperty_LYT_Tiles asserts that any layout tiles the screen without
// overlap.
func TestProperty_LYT_Tiles(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 50000; i++ {
		checkTiles(t, Inputs{
			Width:       rng.Intn(400),
			Height:      rng.Intn(400),
			EditorLines: rng.Intn(30) - 3,
			AboveLines:  rng.Intn(30) - 3,
			BelowLines:  rng.Intn(30) - 3,
			WantSidebar: rng.Intn(2) == 0,
		})
	}
}

// checkTiles asserts that a layout's rectangles never overlap, always lie
// inside the clamped screen, and tile its height exactly.
func checkTiles(t *testing.T, in Inputs) {
	t.Helper()
	r := Compute(in)
	w, h := in.Width, in.Height
	if w < 20 {
		w = 20
	}
	if h < 0 {
		h = 0
	}
	named := []struct {
		name string
		rect Rect
	}{
		{"header", r.Header}, {"main", r.Main}, {"above", r.Above},
		{"editor", r.Editor}, {"below", r.Below}, {"footer", r.Footer},
		{"sidebar", r.Sidebar},
	}
	for _, n := range named {
		if n.rect.W < 0 || n.rect.H < 0 {
			t.Fatalf("%v: %s negative size %+v", in, n.name, n.rect)
		}
		if n.rect.X < 0 || n.rect.Y < 0 || n.rect.X+n.rect.W > w || n.rect.Y+n.rect.H > h {
			t.Fatalf("%v: %s outside the %dx%d screen: %+v", in, n.name, w, h, n.rect)
		}
	}
	for i := 0; i < len(named); i++ {
		for j := i + 1; j < len(named); j++ {
			if overlaps(named[i].rect, named[j].rect) {
				t.Fatalf("%v: %s overlaps %s (%+v, %+v)", in, named[i].name, named[j].name, named[i].rect, named[j].rect)
			}
		}
	}
	stack := []Rect{r.Header, r.Main, r.Above, r.Editor, r.Below, r.Footer}
	if sum := r.Header.H + r.Main.H + r.Above.H + r.Editor.H + r.Below.H + r.Footer.H; sum != h {
		t.Fatalf("%v: vertical regions sum to %d, want %d", in, sum, h)
	}
	y := 0
	for _, rect := range stack {
		if rect.Y != y {
			t.Fatalf("%v: vertical region does not start at %d: %+v", in, y, rect)
		}
		y += rect.H
	}
}

func overlaps(a, b Rect) bool {
	if a.W == 0 || a.H == 0 || b.W == 0 || b.H == 0 {
		return false
	}
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}
