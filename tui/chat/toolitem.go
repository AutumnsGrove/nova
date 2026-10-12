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
	display  *Display
	call     render.Call
}

func newToolItem(st styles.Styles, registry *render.Registry, display *Display, id, callID string, call *proto.ToolCall, now time.Time) *toolItem {
	return &toolItem{
		base:     &base{id: id},
		st:       st,
		registry: registry,
		display:  display,
		call: render.Call{
			ID:      callID,
			Name:    call.Name,
			Args:    call.Arguments,
			Now:     now,
			Started: now,
		},
	}
}

// Render draws the card: always a header, then a body when the call is
// expanded, running, or an error.
func (ti *toolItem) Render(width int) string {
	reg := ti.registry
	if reg == nil {
		reg = render.NewRegistry()
	}
	rd := reg.Lookup(ti.call.Name)
	expanded := ti.display != nil && ti.display.ExpandTools
	header := rd.Header(ti.call, ti.st, width)
	body := rd.Body(ti.call, ti.st, width, expanded)
	if body == "" {
		return header
	}
	return header + "\n" + body
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
