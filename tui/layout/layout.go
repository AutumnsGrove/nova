//go:build !notui

// Package layout computes the rectangles of the TUI screen from the terminal
// size and the desired heights of the editor and widgets. It is pure and has
// no I/O so it can be tested exhaustively.
package layout

// Rect is a rectangle in cells, with the origin at the top left.
type Rect struct{ X, Y, W, H int }

// Inputs describes the screen and the desired region sizes.
type Inputs struct {
	Width, Height int
	BannerLines   int // desired height of the splash banner, 0 to MaxBannerLines
	StatusLines   int // desired height of the status region, 0 means the one-line default
	EditorLines   int // desired height of the editor, 1 to MaxEditorLines
	AboveLines    int // widgets above the editor
	BelowLines    int
	WantSidebar   bool
}

// Regions is the computed layout. A region with zero width or height is not
// shown.
type Regions struct{ Banner, Header, Main, Above, Editor, Below, Footer, Sidebar Rect }

// Compute returns the layout for the given inputs. It never returns a
// negative size.
func Compute(in Inputs) Regions {
	width, height := in.Width, in.Height
	if width < MinWidth {
		width = MinWidth
	}
	if height < 0 {
		height = 0
	}

	short := height < shortScreen // under 8 lines: no widgets, header, or sidebar

	headerH := 0
	if height >= headerMin {
		headerH = 1
	}
	if short {
		headerH = 0
	}
	statusH := StatusHeight(height, in.StatusLines)

	// The banner is the top region while the splash plays. It is dropped before
	// the header when the screen cannot hold it above the main floor and one
	// editor row.
	bannerH := 0
	if !short && in.BannerLines > 0 {
		bannerH = clamp(in.BannerLines, 1, MaxBannerLines)
		if bannerH > BannerBudget(height, statusH) {
			bannerH = 0
		}
	}

	// remaining is the space left for main, widgets, and the editor after the
	// banner, header, and status region are placed.
	remaining := height - bannerH - headerH - statusH
	if remaining < 0 {
		remaining = 0
	}

	// mainMin is the main area's floor: three lines where the screen allows,
	// less only when the screen is too short for even that.
	mainMin := mainFloor
	if remaining < mainMin {
		mainMin = remaining
	}
	space := remaining - mainMin // room for the editor and widgets

	editorWant := clamp(in.EditorLines, 1, MaxEditorLines)
	aboveWant := clamp(in.AboveLines, 0, 8)
	belowWant := clamp(in.BelowLines, 0, 8)
	if short {
		aboveWant, belowWant = 0, 0
	}

	// The editor keeps its desired height first; widgets take what remains, so
	// widgets are sacrificed before the editor as the screen shrinks.
	editorH := editorWant
	if editorH > space {
		editorH = space
	}
	leftover := space - editorH
	aboveH := aboveWant
	if aboveH > leftover {
		aboveH = leftover
	}
	belowH := belowWant
	if belowH > leftover-aboveH {
		belowH = leftover - aboveH
	}
	mainH := remaining - editorH - aboveH - belowH

	// The sidebar is carved out of the right of the main band and only exists
	// on a wide, tall-enough screen.
	mainW := width
	sidebar := Rect{}
	if in.WantSidebar && width >= sidebarMin && !short {
		mainW = width - sidebarWidth
		sidebar = Rect{X: mainW, Y: bannerH + headerH, W: sidebarWidth, H: mainH}
	}

	return Regions{
		Banner:  Rect{X: 0, Y: 0, W: width, H: bannerH},
		Header:  Rect{X: 0, Y: bannerH, W: width, H: headerH},
		Main:    Rect{X: 0, Y: bannerH + headerH, W: mainW, H: mainH},
		Above:   Rect{X: 0, Y: bannerH + headerH + mainH, W: width, H: aboveH},
		Editor:  Rect{X: 0, Y: bannerH + headerH + mainH + aboveH, W: width, H: editorH},
		Below:   Rect{X: 0, Y: bannerH + headerH + mainH + aboveH + editorH, W: width, H: belowH},
		Footer:  Rect{X: 0, Y: height - statusH, W: width, H: statusH},
		Sidebar: sidebar,
	}
}

// Layout thresholds, named so the behaviour is readable and testable.
const (
	// MinWidth is the smallest width the layout uses. Narrower screens are laid
	// out as if they were this wide, so no rectangle ever goes negative.
	MinWidth = 20
	// MaxEditorLines is the tallest the editor may grow. Above it the editor
	// keeps this height and scrolls with the cursor.
	MaxEditorLines = 10
	// MaxBannerLines is the tallest the splash banner may be: the splash's Full
	// tier is 21 rows. A screen too short for a request still drops the banner.
	MaxBannerLines = 21
	// MaxStatusLines is the tallest the status region may be. A short screen
	// keeps one or two rows rather than squeezing the transcript.
	MaxStatusLines = 8

	mainFloor    = 3  // the main area never drops below this where the screen allows
	shortScreen  = 8  // under this, widgets, banner, header, and sidebar are dropped
	headerMin    = 12 // the header appears only at or above this height
	sidebarMin   = 120
	sidebarWidth = 32
)

// clamp limits v to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// BannerBudget is the tallest banner a screen of this height can hold with the
// status region already reserved, without pushing the main area below its
// three-line floor or the editor below one row. It is 0 when there is no room.
func BannerBudget(height, statusH int) int {
	if height < shortScreen {
		return 0
	}
	headerH := 0
	if height >= headerMin {
		headerH = 1
	}
	return max(height-headerH-statusH-mainFloor-1, 0)
}

// StatusHeight is the status region height for a screen, given the configured
// want. Zero means the one-line default; a short screen keeps at most two rows,
// a tiny one keeps one, and the region never squeezes the main floor or the
// editor.
func StatusHeight(height, want int) int {
	if height == 0 {
		return 0
	}
	h := 1
	if want > 0 {
		h = clamp(want, 1, MaxStatusLines)
	}
	if height < shortScreen {
		h = 1
	} else if height < headerMin {
		h = min(h, 2)
	}
	return min(h, StatusBudget(height))
}

// StatusBudget is the tallest status region a screen can hold above the main
// floor and one editor row.
func StatusBudget(height int) int {
	if height == 0 {
		return 0
	}
	headerH := 0
	if height >= headerMin {
		headerH = 1
	}
	return max(height-headerH-mainFloor-1, 0)
}
