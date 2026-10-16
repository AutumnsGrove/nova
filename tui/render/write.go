//go:build !notui

package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// writeRenderer draws the write tool: what was written and how big it was.
type writeRenderer struct{}

func (writeRenderer) Header(c Call, st styles.Styles, width int) string {
	left := statusDot(c, st) + " " + st.Bold.Render("write")
	if p := pathOf(c); p != "" {
		left += " " + st.Text.Render(elidePath(p, width))
	}
	verb := "overwrote"
	if detailBool(c.Details, "created") {
		verb = "created"
	}
	verb += ", " + strconv.Itoa(intOr0(c.Details, "bytes")) + " bytes"
	left += " " + st.Muted.Render("("+verb+")")
	if c.Truncated {
		left += " " + st.Muted.Render("(truncated)")
	}
	return oneLine(left, durationText(c), width)
}

func (writeRenderer) Body(c Call, st styles.Styles, width int, expanded bool) string {
	if c.IsError {
		return errorBody(c, st, width)
	}
	if c.Running {
		return renderText(c.Live, st, width, 5)
	}
	lines := Highlight(argString(c.Args, "content"), pathOf(c), st, width)
	if !expanded {
		return clipPreview(lines, st, width, false, previewCap(c))
	}
	return capLines(lines, 200, st)
}

// intOr0 decodes an int detail, or zero.
func intOr0(details []byte, key string) int {
	n, _ := detailInt(details, key)
	return n
}

// capLines caps highlighted lines with a muted tail.
func capLines(lines []string, max int, st styles.Styles) string {
	if len(lines) > max {
		extra := len(lines) - max
		lines = append(lines[:max], st.Muted.Render(fmt.Sprintf("… %d more lines", extra)))
	}
	return strings.Join(lines, "\n")
}
