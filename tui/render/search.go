//go:build !notui

package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// searchRenderer draws glob, grep, and list: an identifying argument and a
// count, and the matching lines when expanded.
type searchRenderer struct{ kind string }

func (s searchRenderer) Header(c Call, st styles.Styles, width int) string {
	left := statusDot(c, st) + " " + st.Bold.Render(s.kind)
	ident := s.ident(c)
	if ident != "" {
		left += " " + st.Text.Render(truncate(ident, width))
	}
	if n, ok := s.count(c); ok {
		left += " " + st.Muted.Render(fmt.Sprintf("· %d %s", n, s.unit()))
	}
	if c.Truncated {
		left += " " + st.Muted.Render("(truncated)")
	}
	return oneLine(left, durationText(c), width)
}

func (s searchRenderer) Body(c Call, st styles.Styles, width int, expanded bool) string {
	if c.IsError {
		return errorBody(c, st, width)
	}
	if c.Running {
		return renderText(c.Live, st, width, 5)
	}
	if !expanded {
		return ""
	}
	lines := strings.Split(ansi.Strip(c.Content), "\n")
	for i, l := range lines {
		if s.kind == "grep" {
			if pat := argString(c.Args, "pattern"); pat != "" {
				l = strings.ReplaceAll(l, pat, st.Core.Render(pat))
			}
		}
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(lines, "\n")
}

// ident is the argument that labels the search.
func (s searchRenderer) ident(c Call) string {
	switch s.kind {
	case "list":
		return argString(c.Args, "path")
	default:
		return argString(c.Args, "pattern")
	}
}

// count reads the count from Details, else counts the content lines.
func (s searchRenderer) count(c Call) (int, bool) {
	key := "count"
	if s.kind == "grep" {
		key = "matches"
	}
	if n, ok := detailInt(c.Details, key); ok {
		return n, true
	}
	if strings.TrimSpace(ansi.Strip(c.Content)) == "" {
		return 0, false
	}
	n := 0
	for _, l := range strings.Split(ansi.Strip(c.Content), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n, true
}

// unit is the counted thing for the header.
func (s searchRenderer) unit() string {
	if s.kind == "grep" {
		return "matches"
	}
	return "files"
}
