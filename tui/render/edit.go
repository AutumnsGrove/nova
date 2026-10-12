//go:build !notui

package render

import (
	"fmt"
	"strings"

	"github.com/AutumnsGrove/nova/tui/diff"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// editRenderer draws the edit tool: added and removed counts in the header and
// a real diff in the body.
type editRenderer struct{}

func (editRenderer) Header(c Call, st styles.Styles, width int) string {
	left := statusDot(c, st) + " " + st.Bold.Render("edit")
	if p := pathOf(c); p != "" {
		left += " " + st.Text.Render(elidePath(p, width))
	}
	left += " " + st.Success.Render(fmt.Sprintf("+%d", intOr0(c.Details, "added"))) +
		" " + st.Error.Render(fmt.Sprintf("−%d", intOr0(c.Details, "removed")))
	if c.Truncated {
		left += " " + st.Muted.Render("(truncated)")
	}
	return oneLine(left, durationText(c), width)
}

func (editRenderer) Body(c Call, st styles.Styles, width int, expanded bool) string {
	if c.IsError {
		return errorBody(c, st, width)
	}
	if c.Running {
		return renderText(c.Live, st, width, 5)
	}
	if detailBool(c.Details, "diff_omitted") {
		return omittedBody(c, st, width)
	}
	unified := detailString(c.Details, "diff")
	if unified == "" {
		return ""
	}
	opt := diff.Options{Wrap: true, ShowNumbers: true}
	if !expanded {
		opt.MaxLines = 12
	}
	return diff.Render(unified, pathOf(c), width, st, opt)
}

// omittedBody shows the replaced and new text when the tool could not diff.
func omittedBody(c Call, st styles.Styles, width int) string {
	edits, _ := argMap(c.Args)["edits"].([]any)
	var out []string
	for _, e := range edits {
		em, _ := e.(map[string]any)
		if old, ok := em["oldText"].(string); ok {
			for _, l := range trimLines(old, 10, st) {
				out = append(out, st.Error.Render("-"+l))
			}
		}
		if newText, ok := em["newText"].(string); ok {
			for _, l := range trimLines(newText, 10, st) {
				out = append(out, st.Success.Render("+"+l))
			}
		}
	}
	return strings.Join(out, "\n")
}

// trimLines splits text into at most max lines, with a muted tail.
func trimLines(s string, max int, st styles.Styles) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > max {
		extra := len(lines) - max
		lines = append(lines[:max], st.Muted.Render(fmt.Sprintf("… %d more lines", extra)))
	}
	return lines
}
