//go:build !notui

package render

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// genericRenderer is the fallback for a tool with no renderer of its own.
type genericRenderer struct{}

func (genericRenderer) Header(c Call, st styles.Styles, width int) string {
	left := statusDot(c, st) + " " + st.Bold.Render(c.Name)
	if s := Summarize(c.Name, c.Args, width); s != "" {
		left += "  " + st.Muted.Render(s)
	}
	if c.Truncated {
		left += " " + st.Muted.Render("(truncated)")
	}
	return oneLine(left, durationText(c), width)
}

func (genericRenderer) Body(c Call, st styles.Styles, width int, expanded bool) string {
	return genericBody(c, st, width, expanded)
}

// genericBody renders a generic result: pretty arguments when expanded, then
// the content.
func genericBody(c Call, st styles.Styles, width int, expanded bool) string {
	show, limit := bodyLimit(c, expanded)
	if !show {
		return ""
	}
	if c.IsError {
		return errorBody(c, st, width)
	}
	var parts []string
	if expanded {
		if args := prettyArgs(c.Args); args != "" {
			parts = append(parts, st.Muted.Render(ansi.Truncate(args, width, "…")))
		}
	}
	text := c.Content
	if c.Running {
		text, limit = lastLines(c.Live, 5), 0
	}
	if strings.TrimSpace(ansi.Strip(text)) != "" {
		if !expanded && !c.Running {
			parts = append(parts, previewText(text, st, width, false, previewCap(c)))
		} else {
			parts = append(parts, renderText(text, st, width, limit))
		}
	}
	if c.Truncated {
		parts = append(parts, st.Muted.Render("(output truncated)"))
	}
	return strings.Join(parts, "\n")
}

// prettyArgs indents a call's JSON arguments.
func prettyArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(args, &v) != nil {
		return clean(string(args))
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return clean(string(args))
	}
	return string(out)
}
