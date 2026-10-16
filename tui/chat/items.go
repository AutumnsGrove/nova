//go:build !notui

package chat

import (
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

// spaced closes a rendered item with one blank line, so transcript blocks never
// run together. An empty item stays empty (it draws nothing).
func spaced(s string) string {
	if s == "" {
		return ""
	}
	return s + "\n"
}

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
	// The user's message sits in its own panel, so it does not read as
	// assistant prose. The label rides the first line; the wrap leaves room
	// for it on every line.
	inner := PanelInner(width)
	content := label + "  " + wrapPlain(body, max(inner-ansi.StringWidth(label)-2, 1))
	return spaced(frame(content, u.st.UserPanel, width))
}

// restyle swaps the theme and refreshes the item.
func (u *userItem) restyle(st styles.Styles) { u.st = st; u.touch() }

// assistantItem is one assistant message. Its text is rendered incrementally
// through the markdown renderer.
type assistantItem struct {
	*base
	st   styles.Styles
	md   *markdown
	done bool
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
	return spaced(a.st.Assistant.Render(rendered))
}

// restyle swaps the theme and refreshes the item.
func (a *assistantItem) restyle(st styles.Styles) { a.st = st; a.touch() }

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
		return spaced(l.st.Error.Render("✖ " + text))
	case "notice":
		return spaced(l.st.Notice.Render(text))
	default:
		return spaced(l.st.Muted.Render(text))
	}
}

// restyle swaps the theme and refreshes the item.
func (l *lineItem) restyle(st styles.Styles) { l.st = st; l.touch() }

// compactionItem marks where the conversation was compacted.
type compactionItem struct {
	*base
	st            styles.Styles
	before, after int
}

func (c *compactionItem) Render(width int) string {
	text := fmt.Sprintf("── compacted %s → %s tokens", shortTokens(c.before), shortTokens(c.after))
	return spaced(c.st.Compaction.Render(wrapPlain(text, width)))
}

// restyle swaps the theme and refreshes the item.
func (c *compactionItem) restyle(st styles.Styles) { c.st = st; c.touch() }

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
