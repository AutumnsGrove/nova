//go:build !notui

// Package styles turns a theme palette into the lipgloss styles the TUI draws
// with. Themes are data: every colour lives here, and Build is the only place
// a style is created, so no component hardcodes a colour.
package styles

// Theme is a named palette. The keys are spark, ink, core, warm, bg, muted,
// success, and error.
type Theme struct {
	Name   string
	Colors map[string]string
}

// Dark is the brand palette for a dark terminal (BRAND.md).
func Dark() Theme {
	return Theme{Name: "nova-dark", Colors: map[string]string{
		"spark":   "#62c6ff",
		"ink":     "#f2f6fb",
		"core":    "#fff4cf",
		"warm":    "#ff9a5c",
		"bg":      "#0b0f16",
		"muted":   "#8b98ad",
		"success": "#7ee0a1",
		"error":   "#ff6b6b",
	}}
}

// Light is a palette with enough contrast for a light terminal.
func Light() Theme {
	return Theme{Name: "nova-light", Colors: map[string]string{
		"spark":   "#0b64c0",
		"ink":     "#101722",
		"core":    "#8a5a00",
		"warm":    "#b23a0d",
		"bg":      "#f7f9fc",
		"muted":   "#4d5866",
		"success": "#186b3a",
		"error":   "#b32020",
	}}
}

// Mono uses no colour at all: the styles it builds use bold, dim, and reverse
// only.
func Mono() Theme {
	return Theme{Name: "mono", Colors: map[string]string{}}
}
