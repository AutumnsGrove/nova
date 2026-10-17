//go:build !notui

package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Display carries the user's rendering preferences. A zero value means the
// defaults; use DefaultDisplay.
type Display struct {
	ThinkingPreviewLines int
	ToolPreviewLines     int
	ExpandThinking       bool
	ExpandTools          bool
	Splash               bool
	// ShowImages draws received images inline when the terminal supports it.
	ShowImages bool
	// ImageWidthCells is the preferred inline image width in terminal cells.
	ImageWidthCells int
}

// DefaultDisplay returns the starting display preferences.
func DefaultDisplay() Display {
	return Display{ThinkingPreviewLines: 8, ToolPreviewLines: 6, Splash: true, ShowImages: true, ImageWidthCells: 60}
}

// previewLines clamps the thinking preview height to 1..50.
func (d Display) previewLines() int {
	return clampLines(d.ThinkingPreviewLines)
}

// toolLines clamps the tool-result preview height to 1..50.
func (d Display) toolLines() int {
	return clampLines(d.ToolPreviewLines)
}

// clampLines clamps a preview height to 1..50.
func clampLines(n int) int {
	if n < 1 {
		return 1
	}
	if n > 50 {
		return 50
	}
	return n
}

// thinkingItem is the native reasoning block. While streaming it shows a timer
// and the tail of the text; finished it is one line, or the full text when
// expanded.
type thinkingItem struct {
	*base
	st       styles.Styles
	r        Renderer
	display  *Display
	text     string
	start    time.Time
	now      time.Time
	ms       int64
	tokens   *int
	done     bool
	second   int
	expanded bool
}

func newThinkingItem(st styles.Styles, r Renderer, display *Display, id string, start time.Time) *thinkingItem {
	return &thinkingItem{base: &base{id: id}, st: st, r: r, display: display, start: start, expanded: display.ExpandThinking}
}

// Render draws the block at width.
func (th *thinkingItem) Render(width int) string {
	if strings.TrimSpace(th.text) == "" {
		return ""
	}
	if width < 1 {
		width = 1
	}
	expanded := th.expanded
	header := th.header(width, expanded)
	head := th.st.Thinking.Render(ansi.Truncate(header, width, "…"))
	// A finished block keeps its reasoning on screen as the same tail preview
	// a streaming one shows, so collapsing never hides the thinking outright.
	var body string
	if expanded {
		body = th.st.Thinking.Render(th.r.Markdown(th.text, width))
	} else {
		body = th.preview(width)
	}
	closing := th.st.Thinking.Render(strings.Repeat("─", width))
	return spaced(head + "\n" + body + "\n" + closing)
}

// header is the one-line label for the block.
func (th *thinkingItem) header(width int, expanded bool) string {
	if !th.done {
		return ruled(fmt.Sprintf("▸ Thinking for %s", th.elapsed()), "", width)
	}
	arrow := "▸"
	if expanded {
		arrow = "▾"
	}
	label := arrow + " Thought"
	if d := th.durationLabel(); d != "" {
		label += " for " + d
	}
	if t := th.tokensText(); t != "" {
		label += " · " + t + " tokens"
	}
	if !expanded {
		return ruled(label, "click to expand", width)
	}
	return ruled(label, "", width)
}

// ruled draws label as a horizontal rule, "── label ──────── hint", the way
// pi frames a thinking block. The hint is dropped when it does not fit.
func ruled(label, hint string, width int) string {
	left := "── " + label + " "
	lw := ansi.StringWidth(left)
	if hint != "" && lw+ansi.StringWidth(hint)+3 <= width {
		fill := width - lw - ansi.StringWidth(hint) - 2
		return left + strings.Repeat("─", fill) + " " + hint
	}
	if lw < width {
		return left + strings.Repeat("─", width-lw)
	}
	return left
}

// preview is the last previewLines wrapped lines, with a leading ellipsis when
// earlier lines are hidden.
func (th *thinkingItem) preview(width int) string {
	lines := strings.Split(wrapPlain(th.text, width), "\n")
	if n := th.display.previewLines(); len(lines) > n {
		lines = append([]string{"…"}, lines[len(lines)-n:]...)
	}
	for i, l := range lines {
		lines[i] = th.st.Thinking.Render(ansi.Truncate(l, width, "…"))
	}
	return strings.Join(lines, "\n")
}

// elapsed is the running duration.
func (th *thinkingItem) elapsed() string {
	if th.start.IsZero() {
		return "0s"
	}
	return render.FormatDuration(th.now.Sub(th.start))
}

// durationLabel is the finished duration, or "" when it is unknown.
func (th *thinkingItem) durationLabel() string {
	if th.ms > 0 {
		return render.FormatDuration(time.Duration(th.ms) * time.Millisecond)
	}
	if !th.start.IsZero() && !th.now.IsZero() {
		return render.FormatDuration(th.now.Sub(th.start))
	}
	return ""
}

// tokensText is the reported or estimated token count, or "".
func (th *thinkingItem) tokensText() string {
	if th.tokens != nil && *th.tokens > 0 {
		return render.FormatTokens(*th.tokens)
	}
	est := len([]rune(th.text)) / 4
	if est == 0 {
		return ""
	}
	return "~" + render.FormatTokens(est)
}

// setNow updates the running clock, bumping the version only when the
// displayed second changes.
func (th *thinkingItem) setNow(now time.Time) bool {
	th.now = now
	if th.done || th.start.IsZero() {
		return false
	}
	s := int(now.Sub(th.start).Seconds())
	if s != th.second {
		th.second = s
		th.touch()
		return true
	}
	return false
}

// rightHint pads a label with a right-aligned hint when it fits.
func rightHint(left, right string, width int) string {
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	if lw+rw+2 > width {
		return left
	}
	return left + strings.Repeat(" ", width-lw-rw) + right
}

// restyle swaps the theme and refreshes the item.
func (th *thinkingItem) restyle(st styles.Styles) { th.st = st; th.touch() }
