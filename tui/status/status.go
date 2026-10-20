//go:build !notui

// Package status renders the multi-line status region below the editor: the
// shipped cwd, token, cost, context and model segments, plus whatever
// extensions publish. It is pure; the UI fills Data and the widget store
// supplies the extension segments.
package status

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/sanitize"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Segment is one status segment: the text and the theme token that styles it.
type Segment struct {
	Text  string
	Style string
}

// Ext is an extension status segment, as the widget store carries it.
type Ext struct {
	Text  string
	Style string
}

// Data is everything the built-in segments read. Every field is already
// resolved by the caller; the package does no I/O.
type Data struct {
	CWD      string
	Branch   string
	GitOK    bool
	Added    int
	Removed  int
	Model    string
	Provider string
	Thinking string

	Prompt     int
	Completion int
	Cached     int
	Cost       float64

	ContextTokens int
	ContextLimit  int

	Session time.Duration
	Block   time.Duration

	Queued int
	Hint   string
	Run    string
}

// maxExtLines bounds the rows extension statuses may occupy.
const maxExtLines = 6

// Default is the shipped layout: cwd and git, then tokens, cost and context,
// then timers and hints with the model on the bottom right.
func Default() config.StatusConfig {
	return config.StatusConfig{
		Separator: " · ",
		ExtLines:  2,
		Lines: []config.StatusLine{
			{Left: []string{"cwd", "branch", "changes"}},
			{Left: []string{"in", "out", "cache_read", "cache_hit", "cost", "ctx", "auto"}},
			{Left: []string{"run", "session", "block", "queued", "hint"}, Right: []string{"provider", "model", "thinking"}},
		},
	}
}

// Compact is a one-line layout for a small screen or a user who wants less.
func Compact() config.StatusConfig {
	return config.StatusConfig{
		Separator: " · ",
		ExtLines:  1,
		Lines: []config.StatusLine{
			{Left: []string{"cwd", "branch", "changes"}, Right: []string{"ctx", "cost", "model"}},
		},
	}
}

// Lines returns the configured lines, or the defaults when none are set.
func Lines(cfg config.StatusConfig) []config.StatusLine {
	if len(cfg.Lines) == 0 {
		return Default().Lines
	}
	return cfg.Lines
}

// Height is the number of rows the region wants: the configured lines plus the
// rows reserved for extension statuses, at least one.
func Height(cfg config.StatusConfig, hasExt bool) int {
	n := len(Lines(cfg))
	if hasExt && cfg.ExtLines > 0 {
		n += min(cfg.ExtLines, maxExtLines)
	}
	return max(n, 1)
}

// Build renders the configured lines and the extension statuses. Every returned
// line fits width. Extension statuses wrap at segment boundaries and are capped
// at ExtLines rows.
func Build(cfg config.StatusConfig, d Data, width int, st styles.Styles, ext []Ext) []string {
	if width <= 0 {
		return nil
	}
	sep := cfg.Separator
	if sep == "" {
		sep = " · "
	}
	d = d.cleaned()
	lines := Lines(cfg)
	out := make([]string, 0, len(lines)+cfg.ExtLines)
	for _, line := range lines {
		out = append(out, renderLine(segments(line.Left, d, st), segments(line.Right, d, st), width, sep, st))
	}
	out = append(out, extensionLines(ext, width, cfg.ExtLines, st)...)
	return out
}

// cleaned returns d with every text field made safe for one line of a
// terminal. The working directory and branch come from the folder the user
// opened, so they can hold bytes a terminal acts on.
func (d Data) cleaned() Data {
	for _, f := range []*string{&d.CWD, &d.Branch, &d.Model, &d.Provider, &d.Thinking, &d.Hint, &d.Run} {
		*f = sanitize.Line(*f)
	}
	return d
}

// segments resolves a list of ids, dropping the ones with no value.
func segments(ids []string, d Data, st styles.Styles) []Segment {
	out := make([]Segment, 0, len(ids))
	for _, id := range ids {
		if seg := resolve(id, d, st); seg.Text != "" {
			out = append(out, seg)
		}
	}
	return out
}

// resolve turns one segment id into its text and style token.
func resolve(id string, d Data, st styles.Styles) Segment {
	switch id {
	case "cwd":
		return Segment{Text: d.CWD, Style: "muted"}
	case "branch":
		if d.GitOK && d.Branch != "" {
			return Segment{Text: d.Branch, Style: "accent"}
		}
	case "changes":
		if d.GitOK {
			return Segment{Text: fmt.Sprintf("(+%d,-%d)", d.Added, d.Removed), Style: "muted"}
		}
	case "session":
		return Segment{Text: "session " + render.FormatDuration(d.Session), Style: "muted"}
	case "block":
		if d.Block > 0 {
			return Segment{Text: "block " + render.FormatDuration(d.Block), Style: "muted"}
		}
	case "in":
		if d.Prompt > 0 {
			return Segment{Text: "↑" + render.FormatTokens(d.Prompt), Style: "text"}
		}
	case "out":
		if d.Completion > 0 {
			return Segment{Text: "↓" + render.FormatTokens(d.Completion), Style: "text"}
		}
	case "cache_read":
		if d.Cached > 0 {
			return Segment{Text: "R" + render.FormatTokens(d.Cached), Style: "text"}
		}
	case "cache_hit":
		if d.Cached > 0 && d.Prompt > 0 {
			pct := float64(d.Cached) / float64(d.Prompt) * 100
			return Segment{Text: fmt.Sprintf("CH%.1f%%", pct), Style: "success"}
		}
	case "cost":
		return Segment{Text: fmt.Sprintf("$%.3f", d.Cost), Style: "muted"}
	case "ctx":
		if d.ContextLimit > 0 {
			pct := float64(d.ContextTokens) / float64(d.ContextLimit) * 100
			style := "text"
			switch {
			case pct > 90:
				style = "error"
			case pct > 70:
				style = "warn"
			}
			return Segment{Text: fmt.Sprintf("%.0f%%/%s", pct, render.FormatTokens(d.ContextLimit)), Style: style}
		}
		return Segment{Text: "ctx n/a", Style: "muted"}
	case "window":
		if d.ContextLimit > 0 {
			return Segment{Text: render.FormatTokens(d.ContextLimit), Style: "muted"}
		}
	case "auto":
		if d.ContextLimit > 0 {
			return Segment{Text: "(auto)", Style: "muted"}
		}
	case "model":
		if d.Model != "" {
			return Segment{Text: d.Model, Style: "bold"}
		}
	case "provider":
		if d.Provider != "" {
			return Segment{Text: "(" + d.Provider + ")", Style: "muted"}
		}
	case "thinking":
		if d.Thinking != "" {
			return Segment{Text: "thinking " + d.Thinking, Style: "muted"}
		}
	case "queued":
		if d.Queued > 0 {
			return Segment{Text: fmt.Sprintf("%d queued", d.Queued), Style: "warn"}
		}
	case "hint":
		if d.Hint == "" {
			return Segment{}
		}
		key, rest := d.Hint, ""
		if i := strings.IndexByte(d.Hint, ' '); i > 0 {
			key, rest = d.Hint[:i], strings.TrimSpace(d.Hint[i+1:])
		}
		text := styleFor("text", st).Render(key)
		if rest != "" {
			text += " " + styleFor("muted", st).Render(rest)
		}
		return Segment{Text: text, Style: "raw"}
	case "run":
		return Segment{Text: d.Run, Style: "spark"}
	}
	return Segment{}
}

// styleFor maps a token to the active theme. An unknown token is muted, and a
// mono or no-colour profile gets no styling at all, so nothing emits an escape.
func styleFor(token string, st styles.Styles) lipgloss.Style {
	if st.Profile <= colorprofile.ASCII || st.Theme.Name == "mono" {
		return lipgloss.NewStyle()
	}
	switch token {
	case "text":
		return st.Text
	case "accent":
		return st.Core
	case "warn":
		return st.Warm
	case "error":
		return st.Error
	case "success":
		return st.Success
	case "dim":
		return st.Dim
	case "bold":
		return st.Bold
	case "spark":
		return st.Spark
	case "raw":
		return lipgloss.NewStyle()
	default:
		return st.Muted
	}
}

// renderLine places the left and right groups on one line. When both do not
// fit, the left is truncated and the right is kept, because the right carries
// the model; only a right group too wide for the line is dropped.
func renderLine(left, right []Segment, width int, sep string, st styles.Styles) string {
	l := join(left, sep, st)
	r := join(right, sep, st)
	switch {
	case r == "":
		return ansi.Truncate(l, width, "…")
	case l == "":
		if rw := ansi.StringWidth(r); rw < width {
			return strings.Repeat(" ", width-rw) + r
		}
		return ansi.Truncate(r, width, "…")
	}
	lw, rw := ansi.StringWidth(l), ansi.StringWidth(r)
	if lw+2+rw <= width {
		return l + strings.Repeat(" ", width-lw-rw) + r
	}
	if available := width - rw - 2; available > 0 {
		return ansi.Truncate(l, available, "…") + "  " + r
	}
	return ansi.Truncate(l, width, "…")
}

// join renders one group with the muted separator between its segments.
func join(segs []Segment, sep string, st styles.Styles) string {
	var b strings.Builder
	n := 0
	for _, s := range segs {
		if s.Text == "" {
			continue
		}
		if n > 0 {
			b.WriteString(styleFor("muted", st).Render(sep))
		}
		b.WriteString(styleFor(s.Style, st).Render(s.Text))
		n++
	}
	return b.String()
}

// extensionLines renders extension statuses, wrapping at segment boundaries.
func extensionLines(ext []Ext, width, maxLines int, st styles.Styles) []string {
	maxLines = min(max(maxLines, 0), maxExtLines)
	if maxLines == 0 || len(ext) == 0 {
		return nil
	}
	styled := make([]string, len(ext))
	for i, e := range ext {
		styled[i] = styleFor(e.Style, st).Render(sanitize.Text(e.Text))
	}
	var lines []string
	cur := ""
	for i, s := range styled {
		part := s
		if i > 0 {
			part = styleFor("muted", st).Render(" /// ") + s
		}
		if cur != "" && ansi.StringWidth(cur+part) > width {
			lines = append(lines, cur)
			cur = s
			continue
		}
		cur += part
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ansi.Truncate(lines[maxLines-1], max(width-1, 0), "") + "…"
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return lines
}
