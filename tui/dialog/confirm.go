//go:build !notui

package dialog

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// confirm is a yes/no question. Its Value is a proto.UIResponse.
type confirm struct {
	id        string
	title     string
	message   string
	yes       bool
	def       bool
	remaining time.Duration
	timed     bool
	last      time.Time
	done      bool
	value     proto.UIResponse
}

// NewConfirm builds a confirm dialog from a ui_request.
func NewConfirm(req proto.UIRequest) Dialog {
	def := false
	if req.Default != nil {
		def = *req.Default
	}
	c := &confirm{id: req.ID, title: req.Title, message: req.Message, yes: def, def: def}
	if req.TimeoutMS != nil && *req.TimeoutMS >= 0 {
		c.timed = true
		c.remaining = time.Duration(*req.TimeoutMS) * time.Millisecond
	}
	if c.title == "" {
		c.title = "Confirm"
	}
	return c
}

// ID returns the request id.
func (c *confirm) ID() string { return c.id }

// finish records the answer and marks the dialog done.
func (c *confirm) finish(ok, cancelled bool) Result {
	c.done = true
	c.value = proto.UIResponse{Type: proto.FrameUIResponse, ID: c.id, OK: ok, Cancelled: cancelled}
	return Result{Done: true, Cancelled: cancelled, Value: c.value}
}

// HandleKey implements the documented confirm keys.
func (c *confirm) HandleKey(k tea.KeyPressMsg) Result {
	switch k.Keystroke() {
	case "y", "Y":
		return c.finish(true, false)
	case "n", "N":
		return c.finish(false, false)
	case "esc":
		return c.finish(false, true)
	case "enter":
		return c.finish(c.yes, false)
	case "left", "right", "tab", "shift+tab":
		c.yes = !c.yes
	case "ctrl+c":
		return c.finish(false, true)
	}
	return Result{}
}

// Tick counts the timeout down and resolves with the default at zero.
func (c *confirm) Tick(now time.Time) bool {
	if !c.timed || c.done {
		return false
	}
	if c.last.IsZero() {
		c.last = now
		return false
	}
	c.remaining -= now.Sub(c.last)
	c.last = now
	if c.remaining <= 0 {
		c.remaining = 0
		return c.finish(c.def, false).Done
	}
	return true
}

// Size returns the desired box size.
func (c *confirm) Size(maxW, maxH int) (int, int) {
	w := min(max(len(c.message)+4, 46), maxW)
	body := c.body(w - 2)
	h := min(len(body)+3, maxH)
	return w, h
}

// body is the dialog's inner content.
func (c *confirm) body(inner int) []string {
	out := wrap(safe(c.message), inner)
	out = append(out, "")
	out = append(out, c.choices())
	return out
}

// choices renders the two options with the selected one bracketed.
func (c *confirm) choices() string {
	if c.yes {
		return "[ Yes ]  No"
	}
	return "  Yes  [ No ]"
}

// View renders the framed dialog.
func (c *confirm) View(w, h int, st styles.Styles) string {
	title := c.title
	if c.timed {
		title = fmt.Sprintf("%s (%ds)", title, int(c.remaining.Seconds()+0.999))
	}
	return frame(title, c.body(w-2), "y/n  ←→ select  enter confirm  esc cancel", w, h, st)
}

// smallScreen is the one-line form shown when the terminal is too small.
func (c *confirm) smallScreen() string { return "allow? y/n" }
