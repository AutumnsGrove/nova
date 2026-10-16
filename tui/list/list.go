//go:build !notui

package list

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// entry is an item plus its cached render at one width. cached is cleared by
// Touch; a changed item Version also invalidates it.
type entry struct {
	item    Item
	width   int
	version int
	lines   []string
	cached  bool
}

// List is a virtualised list with a viewport. The zero value is not usable;
// call New.
type List struct {
	items     []*entry
	width     int
	height    int
	following bool
	topIdx    int
	topOff    int
	renders   int
}

// New returns an empty list that follows new content.
func New() *List { return &List{following: true} }

// SetItems replaces every item and returns to following the tail.
func (l *List) SetItems(items []Item) {
	l.items = l.items[:0]
	for _, it := range items {
		l.items = append(l.items, &entry{item: it})
	}
	l.following = true
	l.topIdx, l.topOff = 0, 0
}

// ItemAt returns the item at viewport line y (0-based), or nil when the line
// is past the end of the content. It is the hit test behind a mouse click.
func (l *List) ItemAt(y int) Item {
	if y < 0 || y >= l.height {
		return nil
	}
	idx, off := l.start()
	for idx < len(l.items) {
		n := len(l.lines(l.items[idx])) - off
		if y < n {
			return l.items[idx].item
		}
		y -= n
		idx++
		off = 0
	}
	return nil
}

// Sync reconciles the list with items, reusing each entry's cached render by
// id. It preserves the following state and, when not following, keeps the item
// at the top of the viewport, so streaming content does not yank the view back
// to the tail. Use SetItems for a replacement that should return to the tail.
func (l *List) Sync(items []Item) {
	byID := make(map[string]*entry, len(l.items))
	for _, e := range l.items {
		byID[e.item.ID()] = e
	}
	anchor := ""
	if !l.following && l.topIdx >= 0 && l.topIdx < len(l.items) {
		anchor = l.items[l.topIdx].item.ID()
	}
	next := make([]*entry, 0, len(items))
	for _, it := range items {
		if e, ok := byID[it.ID()]; ok {
			e.item = it
			next = append(next, e)
			continue
		}
		next = append(next, &entry{item: it})
	}
	l.items = next
	if l.following || anchor == "" {
		return
	}
	for i, e := range l.items {
		if e.item.ID() == anchor {
			l.topIdx = i
			if h := len(l.lines(e)); l.topOff > h {
				l.topOff = h
			}
			return
		}
	}
	// The anchor item is gone; clamp to the tail.
	l.topIdx, l.topOff = l.tail()
}

// Append adds one item to the end.
func (l *List) Append(it Item) { l.items = append(l.items, &entry{item: it}) }

// Touch invalidates an item's cached render by id. Its Version need not change.
func (l *List) Touch(id string) {
	for _, e := range l.items {
		if e.item.ID() == id {
			e.cached = false
			return
		}
	}
}

// Len returns the number of items.
func (l *List) Len() int { return len(l.items) }

// Resize sets the viewport size. Cached renders at the old width are kept but
// ignored until the item is visible again.
func (l *List) Resize(width, height int) {
	l.width, l.height = width, height
}

// Following reports whether new content scrolls into view.
func (l *List) Following() bool { return l.following }

// SetFollowing turns auto-scroll on or off. Turning it off freezes the current
// view.
func (l *List) SetFollowing(f bool) {
	if f {
		l.following = true
		return
	}
	if l.following {
		l.topIdx, l.topOff = l.tail()
	}
	l.following = false
}

// RenderCount returns the number of item renders since creation. It exists for
// tests and benchmarks.
func (l *List) RenderCount() int { return l.renders }

// ScrollBy moves the viewport by delta lines. A positive delta scrolls toward
// newer content; non-positive toward older. The value is clamped to the
// content, and reaching the bottom resumes following.
func (l *List) ScrollBy(delta int) {
	if delta == 0 {
		return
	}
	idx, off := l.start()
	if delta > 0 {
		idx, off = l.moveDown(idx, off, delta)
		if tail, to := l.tail(); idx > tail || (idx == tail && off >= to) {
			l.following = true
			return
		}
		l.following = false
		l.topIdx, l.topOff = idx, off
		return
	}
	idx, off = l.moveUp(idx, off, -delta)
	l.following = false
	l.topIdx, l.topOff = idx, off
}

// ScrollToBottom jumps to the tail and resumes following.
func (l *List) ScrollToBottom() { l.following = true }

// ScrollToTop jumps to the first line and stops following.
func (l *List) ScrollToTop() {
	l.following = false
	l.topIdx, l.topOff = 0, 0
}

// View returns exactly Height lines, each at most Width cells wide and padded
// with blanks, drawing only the items that intersect the viewport.
func (l *List) View() string {
	if l.height <= 0 {
		return ""
	}
	idx, off := l.start()
	out := make([]string, 0, l.height)
	for idx < len(l.items) && len(out) < l.height {
		lines := l.lines(l.items[idx])
		for off < len(lines) && len(out) < l.height {
			out = append(out, pad(lines[off], l.width))
			off++
		}
		idx++
		off = 0
	}
	blank := strings.Repeat(" ", max(l.width, 0))
	for len(out) < l.height {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}

// start is the first visible (item, line) position. A position left over from
// taller or removed items is clamped, so a viewport is never stuck on a blank
// screen after the content changes height.
func (l *List) start() (int, int) {
	if l.following || l.topIdx < 0 || l.topIdx >= len(l.items) {
		return l.tail()
	}
	if h := len(l.lines(l.items[l.topIdx])); l.topOff > h {
		l.topOff = h
	}
	return l.topIdx, l.topOff
}

// tail is the (item, line) position that puts the last Height lines in view.
func (l *List) tail() (int, int) {
	need := l.height
	i := len(l.items)
	for i > 0 && need > 0 {
		i--
		need -= len(l.lines(l.items[i]))
	}
	if need < 0 {
		return i, -need
	}
	return i, 0
}

// moveDown advances a position by delta lines, clamping at the end.
func (l *List) moveDown(idx, off, delta int) (int, int) {
	for idx < len(l.items) && delta > 0 {
		h := len(l.lines(l.items[idx]))
		if off+delta < h {
			off += delta
			return idx, off
		}
		delta -= h - off
		idx++
		off = 0
	}
	if idx >= len(l.items) {
		return len(l.items), 0
	}
	return idx, off
}

// moveUp retreats a position by mag lines, clamping at the start.
func (l *List) moveUp(idx, off, mag int) (int, int) {
	for mag > 0 {
		if off > 0 {
			if off >= mag {
				return idx, off - mag
			}
			mag -= off
			off = 0
		}
		if idx == 0 {
			return 0, 0
		}
		idx--
		off = len(l.lines(l.items[idx]))
	}
	return idx, off
}

// lines returns the cached render of an entry, re-rendering it only when the
// width or its Version changed.
func (l *List) lines(e *entry) []string {
	if e.cached && e.width == l.width && e.version == e.item.Version() {
		return e.lines
	}
	var lines []string
	if rendered := e.item.Render(l.width); rendered != "" {
		lines = strings.Split(rendered, "\n")
	}
	e.lines, e.width, e.version, e.cached = lines, l.width, e.item.Version(), true
	l.renders++
	return lines
}

// pad truncates a rendered line to width cells and pads it with spaces.
func pad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = ansi.Truncate(s, width, "")
	if w := ansi.StringWidth(s); w < width {
		s += strings.Repeat(" ", width-w)
	}
	return s
}
