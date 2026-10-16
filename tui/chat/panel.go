//go:build !notui

package chat

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// PanelInner is the content width inside a panel of the given outer width:
// one cell of padding on each side. It never returns less than one.
func PanelInner(width int) int {
	if inner := width - 2; inner > 1 {
		return inner
	}
	return 1
}

// Panel wraps a rendered tool card in a full-width, state-tinted background
// with one cell of padding on each side. The card is expected to be drawn at
// PanelInner(width). The background is re-asserted after every reset inside the
// card, so a styled segment (a diff line, a bold header) cannot clear it, and
// every line ends with a reset so the panel cannot bleed into the next item.
// Under Mono or a colourless profile the style carries no background and the
// card is only padded.
func Panel(card string, c render.Call, st styles.Styles, width int) string {
	if card == "" || width <= 2 {
		return card
	}
	inner := PanelInner(width)
	bg := backgroundSeq(panelStyle(c, st))
	lines := strings.Split(card, "\n")
	for i, line := range lines {
		line = ansi.Truncate(line, inner, "…")
		if pad := inner - ansi.StringWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		lines[i] = paint(" "+line+" ", bg)
	}
	return strings.Join(lines, "\n")
}

// panelStyle is the card's background by state: pending while it runs, error on
// failure, success otherwise.
func panelStyle(c render.Call, st styles.Styles) lipgloss.Style {
	switch {
	case c.Running:
		return st.ToolPanel
	case c.IsError:
		return st.ToolPanelError
	default:
		return st.ToolPanelOK
	}
}

// backgroundSeq returns the SGR sequence that turns a style's background on, or
// "" when it has none (Mono or a colourless profile). It reads the sequence
// from the style itself so the profile's degradation is respected.
func backgroundSeq(style lipgloss.Style) string {
	const marker = "\x00"
	rendered := style.Render(marker)
	i := strings.Index(rendered, marker)
	if i <= 0 {
		return ""
	}
	return rendered[:i]
}

// paint puts the panel background behind s and re-asserts it after every reset
// within s. An empty bg is a no-op, so Mono and colourless output are unchanged
// and carry no escapes to reset.
func paint(s, bg string) string {
	if bg == "" {
		return s
	}
	s = strings.ReplaceAll(s, ansi.ResetStyle, ansi.ResetStyle+bg)
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+bg)
	return bg + s + ansi.ResetStyle
}
