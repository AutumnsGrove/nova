//go:build !notui

package chat

import (
	"time"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// toolItem is one tool call card. It holds a render.Call and delegates drawing
// to the registry, so a new tool renders through the generic card with no TUI
// change.
type toolItem struct {
	*base
	st       styles.Styles
	registry *render.Registry
	call     render.Call
	expanded bool
}

func newToolItem(st styles.Styles, registry *render.Registry, display *Display, id, callID string, call *proto.ToolCall, now time.Time) *toolItem {
	return &toolItem{
		base:     &base{id: id},
		st:       st,
		registry: registry,
		call: render.Call{
			ID:      callID,
			Name:    call.Name,
			Args:    call.Arguments,
			Now:     now,
			Started: now,
		},
		expanded: display.ExpandTools,
	}
}

// Render draws the card: a full-width, state-tinted panel holding a header and,
// when expanded, running, or on error, a body.
func (ti *toolItem) Render(width int) string {
	reg := ti.registry
	if reg == nil {
		reg = render.NewRegistry()
	}
	rd := reg.Lookup(ti.call.Name)
	// The panel pads one cell on each side, so the card is drawn to the inner
	// width and then framed.
	inner := PanelInner(width)
	header := rd.Header(ti.call, ti.st, inner)
	body := rd.Body(ti.call, ti.st, inner, ti.expanded)
	card := header
	if body != "" {
		card = header + "\n" + body
	}
	return spaced(Panel(card, ti.call, ti.st, width))
}

// setNow updates the running clock.
func (ti *toolItem) setNow(now time.Time) bool {
	ti.call.Now = now
	if ti.call.Running {
		ti.touch()
		return true
	}
	return false
}

// restyle swaps the theme and refreshes the item.
func (ti *toolItem) restyle(st styles.Styles) { ti.st = st; ti.touch() }
