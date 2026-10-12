//go:build !notui

package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// base is the identity and version every transcript item shares. An item's
// Version changes whenever its content changes, which invalidates its cached
// render in the list.
type base struct {
	id      string
	version int
}

func (b *base) ID() string   { return b.id }
func (b *base) Version() int { return b.version }
func (b *base) touch()       { b.version++ }

// wrapPlain wraps plain text to width, safely. It is the cheap path: no
// markdown, no styling, and ANSI in the source is stripped.
func wrapPlain(s string, width int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(ansi.Strip(s), "\n")
	for i, line := range lines {
		lines[i] = wrap(expandTabs(line, 4), width)
	}
	return strings.Join(lines, "\n")
}

// attachmentSummary renders the attachment names on one muted line.
func attachmentSummary(atts []proto.Attachment) string {
	var names []string
	for _, a := range atts {
		switch {
		case a.Name != "":
			names = append(names, a.Name)
		case a.Path != "":
			names = append(names, a.Path)
		case a.Kind != "":
			names = append(names, a.Kind)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "[attached: " + strings.Join(names, ", ") + "]"
}

// userItem is a message from the user. queued marks a steering or follow-up
// message that has not been consumed yet.
type userItem struct {
	*base
	st          styles.Styles
	text        string
	attachments []string
	queued      bool
}

func newUserItem(st styles.Styles, id, text string, atts []string, queued bool) *userItem {
	return &userItem{base: &base{id: id}, st: st, text: text, attachments: atts, queued: queued}
}

func (u *userItem) Render(width int) string {
	label := u.st.User.Render("you")
	if u.queued {
		label = u.st.Muted.Render("queued")
	}
	body := u.text
	if len(u.attachments) > 0 {
		body += "\n" + strings.Join(u.attachments, " ")
	}
	return label + "  " + wrapPlain(body, max(width-5, 1))
}

// assistantItem is one assistant message. Its text is rendered incrementally
// through the markdown renderer.
type assistantItem struct {
	*base
	st       styles.Styles
	md       *markdown
	thinking string
	done     bool
}

func newAssistantItem(st styles.Styles, r Renderer, id string) *assistantItem {
	return &assistantItem{base: &base{id: id}, st: st, md: newMarkdown(r)}
}

func (a *assistantItem) Render(width int) string {
	if a.md == nil {
		return ""
	}
	a.md.setWidth(width)
	rendered := a.md.String()
	if rendered == "" {
		return ""
	}
	return a.st.Assistant.Render(rendered)
}

// toolItem is a generic tool call card. C16 adds per-tool renderers; here every
// tool renders the name, a one-line argument summary, its status, and output.
type toolItem struct {
	*base
	st      styles.Styles
	callID  string
	name    string
	args    string
	status  string // running | done | error
	output  string
	result  string
	isError bool
}

func newToolItem(st styles.Styles, id, callID string, call *proto.ToolCall) *toolItem {
	return &toolItem{
		base:   &base{id: id},
		st:     st,
		callID: callID,
		name:   call.Name,
		args:   summarizeArgs(call.Arguments),
		status: "running",
	}
}

func (t *toolItem) Render(width int) string {
	dot, style := "●", t.st.Tool
	if t.status == "error" || t.isError {
		dot, style = "✖", t.st.ToolError
	}
	header := dot + " " + t.name
	if t.args != "" {
		header += "  " + t.args
	}
	if t.status == "running" {
		header += "  …"
	}
	out := []string{style.Render(wrapPlain(header, width))}
	body := t.output
	if t.result != "" {
		body = t.result
	}
	if strings.TrimSpace(body) != "" {
		out = append(out, t.st.Muted.Render(wrapPlain(body, max(width-2, 1))))
	}
	return strings.Join(out, "\n")
}

// lineItem is a one-line entry: an error, a retry notice, a closing line, or
// an orphan tool result.
type lineItem struct {
	*base
	st   styles.Styles
	text string
	kind string // error | notice | muted
}

func (l *lineItem) Render(width int) string {
	text := wrapPlain(l.text, width)
	switch l.kind {
	case "error":
		return l.st.Error.Render("✖ " + text)
	case "notice":
		return l.st.Notice.Render(text)
	default:
		return l.st.Muted.Render(text)
	}
}

// compactionItem marks where the conversation was compacted.
type compactionItem struct {
	*base
	st            styles.Styles
	before, after int
}

func (c *compactionItem) Render(width int) string {
	text := fmt.Sprintf("── compacted %s → %s tokens", shortTokens(c.before), shortTokens(c.after))
	return c.st.Compaction.Render(wrapPlain(text, width))
}

// summarizeArgs renders a one-line summary of a tool call's arguments.
func summarizeArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return truncateRaw(string(raw))
	}
	for _, key := range []string{"path", "command", "pattern", "query", "url", "name"} {
		if v, ok := m[key]; ok {
			return key + ": " + compactValue(v)
		}
	}
	for _, key := range sortedKeys(m) {
		return key + ": " + compactValue(m[key])
	}
	return ""
}

// compactValue renders one argument value on a single line.
func compactValue(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	return strings.ReplaceAll(s, "\n", " ")
}

// truncateRaw shortens an unparsable argument string to one line.
func truncateRaw(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	return s
}

// sortedKeys returns a map's keys in a stable order.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// shortTokens renders a token count compactly, e.g. 38k.
func shortTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
