//go:build !notui

package dialog

import (
	"encoding/json"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/sanitize"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// permission is the three-way approval prompt. Its Value is a UIResponse.
type permission struct {
	id       string
	title    string
	tool     string
	reason   string
	args     json.RawMessage
	remember bool
	choice   int // 0 allow once, 1 allow session, 2 deny
	expanded bool
	done     bool
	value    proto.UIResponse
}

// NewPermission builds a permission dialog. expanded starts the arguments
// view open.
func NewPermission(req proto.UIRequest, expanded bool) Dialog {
	p := &permission{
		id:       req.ID,
		title:    req.Title,
		tool:     req.Tool,
		reason:   req.Message,
		args:     req.Args,
		remember: req.Remember,
		expanded: expanded,
		choice:   2,
	}
	if req.Default != nil && *req.Default {
		p.choice = 0
	}
	if p.title == "" {
		p.title = "Allow " + p.tool + "?"
	}
	return p
}

// ID returns the request id.
func (p *permission) ID() string { return p.id }

// options is the list of selectable choices.
func (p *permission) options() []string {
	out := []string{"Allow once (y)"}
	if p.remember {
		out = append(out, "Allow for this session (s)")
	}
	return append(out, "Deny (n)")
}

// denyIndex is the position of the Deny choice.
func (p *permission) denyIndex() int { return len(p.options()) - 1 }

// finish records the chosen answer.
func (p *permission) finish(choice int, cancelled bool) Result {
	p.done = true
	p.value = proto.UIResponse{Type: proto.FrameUIResponse, ID: p.id}
	switch choice {
	case 0:
		p.value.OK = true
	case 1:
		p.value.OK = true
		if p.remember {
			p.value.Remember = "session"
		} else {
			p.value.OK = false
		}
	default:
		p.value.OK = false
		p.value.Cancelled = cancelled
	}
	return Result{Done: true, Cancelled: p.value.Cancelled, Value: p.value}
}

// HandleKey implements the permission keys.
func (p *permission) HandleKey(k tea.KeyPressMsg) Result {
	switch k.Keystroke() {
	case "y", "Y":
		return p.finish(0, false)
	case "s", "S":
		if p.remember {
			return p.finish(1, false)
		}
	case "n", "N":
		return p.finish(p.denyIndex(), false)
	case "esc", "ctrl+c":
		return p.finish(p.denyIndex(), true)
	case "enter":
		return p.finish(p.choice, false)
	case "tab", "shift+tab":
		p.expanded = !p.expanded
	case "up":
		if p.choice > 0 {
			p.choice--
		}
	case "down":
		if p.choice < p.denyIndex() {
			p.choice++
		}
	}
	return Result{}
}

// Tick never times a permission prompt out.
func (p *permission) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (p *permission) Size(maxW, maxH int) (int, int) {
	w := min(max(len(p.reason)+4, 48), maxW)
	body := p.body(w - 2)
	return w, min(len(body)+3, maxH)
}

// body is the dialog's inner content.
func (p *permission) body(inner int) []string {
	var out []string
	if p.tool != "" {
		out = append(out, "tool: "+sanitize.Text(p.tool))
	}
	if sum := render.Summarize(p.tool, p.args, inner-6); sum != "" {
		out = append(out, "      "+sanitize.Text(sum))
	}
	if p.reason != "" {
		out = append(out, wrap(sanitize.Text(p.reason), inner)...)
	}
	out = append(out, "")
	for i, opt := range p.options() {
		marker := "  "
		if i == p.choice {
			marker = "> "
		}
		out = append(out, marker+opt)
	}
	if p.expanded {
		out = append(out, "", "arguments:")
		out = append(out, prettyArgs(p.args, 30)...)
	}
	return out
}

// View renders the framed dialog.
func (p *permission) View(w, h int, st styles.Styles) string {
	hint := "y once · s session · n deny · tab args"
	if !p.remember {
		hint = "y allow · n deny · tab args"
	}
	return frame(p.title, p.body(w-2), hint, w, h, st)
}

// smallScreen is the one-line form shown when the terminal is too small.
func (p *permission) smallScreen() string { return "allow? y/n" }

// optionSummary renders the options for tests.
func (p *permission) optionSummary() string { return strings.Join(p.options(), " | ") }
