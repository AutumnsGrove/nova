//go:build !notui

package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// bashRenderer draws the bash tool: the command, its output, and its status.
type bashRenderer struct{}

func (bashRenderer) Header(c Call, st styles.Styles, width int) string {
	cmd := argString(c.Args, "command")
	ellipsis := ""
	if strings.Contains(cmd, "\n") {
		ellipsis = " …"
	}
	left := statusDot(c, st) + " " + st.Bold.Render("$ "+firstLine(cmd)+ellipsis)
	if code, ok := exitCode(c.Details); ok && code != 0 {
		left += " " + st.Error.Render(fmt.Sprintf("exit %d", code))
	}
	if c.Truncated {
		left += " " + st.Muted.Render("(truncated)")
	}
	return oneLine(left, durationText(c), width)
}

func (bashRenderer) Body(c Call, st styles.Styles, width int, expanded bool) string {
	if c.IsError {
		return errorBody(c, st, width)
	}
	if c.Running {
		return renderText(lastLines(c.Live, 5), st, width, 0)
	}
	if !expanded {
		return previewText(c.Content, st, width, true, previewCap(c))
	}
	var parts []string
	if cmd := argString(c.Args, "command"); cmd != "" {
		parts = append(parts, st.Muted.Render(renderText(cmd, st, width, 0)))
	}
	if strings.TrimSpace(ansi.Strip(c.Content)) == "" {
		parts = append(parts, st.Muted.Render("(no output)"))
	} else {
		parts = append(parts, renderText(lastLines(c.Content, 50), st, width, 0))
	}
	if code, ok := exitCode(c.Details); ok {
		style := st.Muted
		if code != 0 {
			style = st.Error
		}
		parts = append(parts, style.Render(fmt.Sprintf("exit %d", code)))
	}
	return strings.Join(parts, "\n")
}

// exitCode decodes Details.exit_code, which may be a number or null.
func exitCode(details []byte) (int, bool) {
	code, ok := detailInt(details, "exit_code")
	return code, ok
}

// lastLines returns the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r", ""), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
