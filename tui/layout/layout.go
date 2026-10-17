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
	EditorLines   int // desired height of the editor, 1 to MaxEditorLines
	AboveLines    int // widgets above the editor
	BelowLines    int
	WantSidebar   bool
}

// Regions is the computed layout. A region with zero width or height is not
// shown.
type Regions struct{ Header, Main, Above, Editor, Below, Footer, Sidebar Rect }

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
	footerH := 1
	if height == 0 {
		footerH = 0
	}

	// remaining is the space left for main, widgets, and the editor after the
	// header and footer are placed.
	remaining := height - headerH - footerH
	if remaining < 0 {
		remaining = 0
	}

	// mainMin is the main area's floor: three lines where the screen allows,
	// less only when the screen is too short for even that.
	mainMin := 3
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
		sidebar = Rect{X: mainW, Y: headerH, W: sidebarWidth, H: mainH}
	}

	return Regions{
		Header:  Rect{X: 0, Y: 0, W: width, H: headerH},
		Main:    Rect{X: 0, Y: headerH, W: mainW, H: mainH},
		Above:   Rect{X: 0, Y: headerH + mainH, W: width, H: aboveH},
		Editor:  Rect{X: 0, Y: headerH + mainH + aboveH, W: width, H: editorH},
		Below:   Rect{X: 0, Y: headerH + mainH + aboveH + editorH, W: width, H: belowH},
		Footer:  Rect{X: 0, Y: height - footerH, W: width, H: footerH},
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

	shortScreen  = 8  // under this, widgets, header, and sidebar are dropped
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
