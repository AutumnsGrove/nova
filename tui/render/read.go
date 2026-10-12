//go:build !notui

package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// readRenderer draws the read tool: a path and range header, and numbered,
// highlighted content.
type readRenderer struct{}

func (readRenderer) Header(c Call, st styles.Styles, width int) string {
	left := statusDot(c, st) + " " + st.Bold.Render("read")
	if p := pathOf(c); p != "" {
		left += " " + st.Text.Render(elidePath(p, width))
	}
	if start, ok := detailInt(c.Details, "start"); ok {
		end := start
		if e, ok := detailInt(c.Details, "end"); ok {
			end = e
		}
		left += " " + st.Muted.Render(fmt.Sprintf("[%d–%d]", start, end))
	}
	if c.Truncated {
		left += " " + st.Muted.Render("(truncated)")
	}
	return oneLine(left, durationText(c), width)
}

func (readRenderer) Body(c Call, st styles.Styles, width int, expanded bool) string {
	if c.IsError {
		return errorBody(c, st, width)
	}
	if !expanded {
		if c.Running {
			return renderText(c.Live, st, width, 5)
		}
		return ""
	}
	path := pathOf(c)
	lines := strings.Split(strings.TrimRight(c.Content, "\n"), "\n")
	var out []string
	for i, l := range lines {
		if i >= 200 {
			out = append(out, st.Muted.Render(fmt.Sprintf("… %d more lines", len(lines)-200)))
			break
		}
		num, code, ok := strings.Cut(l, "\t")
		if !ok {
			out = append(out, ansi.Truncate(ansi.Strip(l), width, ""))
			continue
		}
		out = append(out, st.Muted.Render(fmt.Sprintf("%4s", num))+" "+ansi.Truncate(highlightCode(code, path, st), width, ""))
	}
	return strings.Join(out, "\n")
}
