//go:build !notui

// Package list is a virtualised, auto-scrolling list of items. It renders only
// the items that intersect the viewport and caches each render, so a
// transcript of thousands of items stays cheap.
package list

// Item is one entry in a List. ID must be stable and unique; Version changes
// whenever the item's content changes. Render is pure and must not do I/O; the
// List caches the result by (ID, width, Version).
type Item interface {
	ID() string
	Version() int
	Render(width int) string
}
