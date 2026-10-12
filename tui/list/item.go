//go:build !notui

// Package list is a virtualised, auto-scrolling list of items. It renders only
// the items that intersect the viewport and caches each render, so a
// transcript of thousands of items stays cheap.
package list

import "strings"

// Item is one entry in a List. ID must be stable and unique; Version changes
// whenever the item's content changes. Render is pure and must not do I/O; the
// List caches the result by (ID, width, Version).
type Item interface {
	ID() string
	Version() int
	Render(width int) string
}

// Text is a simple Item holding fixed lines, useful for plain entries and in
// tests. Bump Version after changing Lines.
type Text struct {
	IDValue      string
	VersionValue int
	Lines        []string
}

// ID returns the item's stable identifier.
func (t *Text) ID() string { return t.IDValue }

// Version returns the item's content version.
func (t *Text) Version() int { return t.VersionValue }

// Render joins the item's lines.
func (t *Text) Render(int) string { return strings.Join(t.Lines, "\n") }
