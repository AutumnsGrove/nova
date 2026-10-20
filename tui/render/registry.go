//go:build !notui

// Package render draws tool calls as cards: a one-line header and an optional
// body. A Registry maps a tool name to its renderer; an unknown or panicking
// renderer falls back to the generic card.
package render

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/sanitize"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// Call is everything a renderer knows about one tool call. Now is the render
// clock, used for the running timer and spinner frame. Preview is the collapsed
// result height the caller set (the user's display setting), or 0 for the
// package default.
type Call struct {
	ID, Name       string
	Args           json.RawMessage
	Running        bool
	Live           string
	Content        string
	IsError        bool
	Truncated      bool
	Details        json.RawMessage
	Started, Ended time.Time
	Now            time.Time
	Preview        int
}

// Renderer draws one tool call. Header is exactly one line; Body is zero or
// more lines.
type Renderer interface {
	Header(c Call, st styles.Styles, width int) string
	Body(c Call, st styles.Styles, width int, expanded bool) string
}

// Registry holds the renderer for each tool name.
type Registry struct {
	renderers map[string]Renderer
	generic   Renderer
}

// NewRegistry returns a registry with every built-in renderer registered.
func NewRegistry() *Registry {
	r := &Registry{renderers: map[string]Renderer{}, generic: genericRenderer{}}
	r.Register("read", readRenderer{})
	r.Register("write", writeRenderer{})
	r.Register("edit", editRenderer{})
	r.Register("bash", bashRenderer{})
	r.Register("glob", searchRenderer{kind: "glob"})
	r.Register("grep", searchRenderer{kind: "grep"})
	r.Register("list", searchRenderer{kind: "list"})
	return r
}

// Register installs a renderer for a tool name.
func (r *Registry) Register(tool string, rd Renderer) { r.renderers[tool] = rd }

// Lookup returns the renderer for a tool name, never nil. The result recovers
// from a panic and falls back to the generic card with a note.
func (r *Registry) Lookup(tool string) Renderer {
	if rd, ok := r.renderers[tool]; ok {
		return &safeRenderer{inner: rd, generic: r.generic}
	}
	return &safeRenderer{inner: r.generic, generic: r.generic}
}

// safeRenderer contains a panicking renderer.
type safeRenderer struct {
	inner, generic Renderer
}

func (s *safeRenderer) Header(c Call, st styles.Styles, width int) (out string) {
	defer func() {
		if recover() != nil {
			out = clipLines(oneLine(s.generic.Header(c, st, width)+" "+st.Muted.Render("(renderer error)"), "", width), width)
		}
	}()
	return clipLines(s.inner.Header(c, st, width), width)
}

func (s *safeRenderer) Body(c Call, st styles.Styles, width int, expanded bool) (out string) {
	defer func() {
		if recover() != nil {
			out = clipLines(s.generic.Body(c, st, width, expanded), width)
		}
	}()
	return clipLines(s.inner.Body(c, st, width, expanded), width)
}

// clipLines truncates every line of a rendered card to width, so no renderer
// can overflow. It is the safety net behind each renderer's own fitting.
func clipLines(s string, width int) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
	}
	return strings.Join(lines, "\n")
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner returns the animation frame for an elapsed duration. The per-block
// status dot and the footer's run indicator share it, so both stay in step.
func Spinner(elapsed time.Duration) string {
	frame := int(elapsed/(100*time.Millisecond)) % len(spinner)
	if frame < 0 {
		frame += len(spinner)
	}
	return spinner[frame]
}

// statusDot is the leading status marker: a spinner while running, a success
// dot when ok, and an error dot otherwise.
func statusDot(c Call, st styles.Styles) string {
	switch {
	case c.Running:
		elapsed := time.Duration(0)
		if !c.Started.IsZero() && !c.Now.IsZero() {
			elapsed = c.Now.Sub(c.Started)
		}
		return st.Spark.Render(Spinner(elapsed))
	case c.IsError:
		return st.Error.Render("✖")
	default:
		return st.Success.Render("●")
	}
}

// durationText is the right-aligned timer: elapsed while running, total when
// finished.
func durationText(c Call) string {
	switch {
	case c.Running && !c.Started.IsZero() && !c.Now.IsZero():
		return FormatDuration(c.Now.Sub(c.Started))
	case !c.Started.IsZero() && !c.Ended.IsZero():
		return FormatDuration(c.Ended.Sub(c.Started))
	default:
		return ""
	}
}

// oneLine joins a left and right part on one line, dropping the right part
// first when space is short, and never exceeding width.
func oneLine(left, right string, width int) string {
	if width < 1 {
		width = 1
	}
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	if right == "" || lw+rw+1 > width {
		return ansi.Truncate(left, width, "…")
	}
	return left + strings.Repeat(" ", width-lw-rw) + right
}

// PreviewLines is the default number of lines of a finished result a collapsed
// card shows when the call does not carry its own limit.
const PreviewLines = 6

// previewCap is the collapsed preview height for a call: the caller's setting
// when set, else the package default.
func previewCap(c Call) int {
	if c.Preview > 0 {
		return c.Preview
	}
	return PreviewLines
}

// bodyLimit is the line cap for a body: none when expanded, fewer while running
// and more for an error.
func bodyLimit(c Call, expanded bool) int {
	switch {
	case expanded:
		return 0
	case c.Running:
		return 5
	case c.IsError:
		return 12
	default:
		return previewCap(c)
	}
}

// moreHint is the muted line that closes a clipped preview. earlier is true
// when the hidden lines come before the shown ones (a tail preview).
func moreHint(n int, earlier bool, st styles.Styles) string {
	which := "more"
	if earlier {
		which = "earlier"
	}
	return st.Muted.Render(fmt.Sprintf("… %d %s lines (click to expand)", n, which))
}

// previewText shows the first limit lines of s, or the last when tail is set,
// with a hint for the rest. It is the compact view of a finished result; an
// empty result gives "".
func previewText(s string, st styles.Styles, width int, tail bool, limit int) string {
	s = strings.TrimRight(sanitize.Text(s), "\n")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return clipPreview(strings.Split(s, "\n"), st, width, tail, limit)
}

// clipPreview truncates each line to width and caps the list at limit.
func clipPreview(lines []string, st styles.Styles, width int, tail bool, limit int) string {
	if hidden := len(lines) - limit; hidden > 0 && tail {
		lines = append([]string{moreHint(hidden, true, st)}, lines[hidden:]...)
	} else if hidden > 0 {
		lines = append(lines[:limit:limit], moreHint(hidden, false, st))
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(lines, "\n")
}

// renderText strips control sequences, caps the line count, and wraps to width.
// A limit of zero means the cap for the body (see bodyLimit) is used, or none.
func renderText(s string, st styles.Styles, width, limit int) string {
	s = sanitize.Text(s)
	lines := strings.Split(s, "\n")
	if limit > 0 && len(lines) > limit {
		extra := len(lines) - limit
		lines = append(lines[:limit], st.Muted.Render(fmt.Sprintf("… %d more lines", extra)))
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(lines, "\n")
}

// errorBody is the first lines of an error result.
func errorBody(c Call, st styles.Styles, width int) string {
	return st.Error.Render(renderText(c.Content, st, width, 12))
}

// argString decodes one string argument from a call's JSON arguments.
func argString(args json.RawMessage, keys ...string) string {
	m := argMap(args)
	for _, k := range keys {
		if v, ok := m[k].(string); ok {
			return v
		}
	}
	return ""
}

// argMap decodes arguments into a map, returning an empty map on error.
func argMap(args json.RawMessage) map[string]any {
	var m map[string]any
	if len(args) == 0 || json.Unmarshal(args, &m) != nil {
		return map[string]any{}
	}
	return m
}

// detailString decodes one string field from Details.
func detailString(details json.RawMessage, key string) string {
	m := argMap(details)
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// detailInt decodes one numeric field from Details.
func detailInt(details json.RawMessage, key string) (int, bool) {
	m := argMap(details)
	switch v := m[key].(type) {
	case float64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	}
	return 0, false
}

// detailBool decodes one boolean field from Details.
func detailBool(details json.RawMessage, key string) bool {
	v, _ := argMap(details)[key].(bool)
	return v
}

// pathOf returns the title path of a call from Details, then arguments.
func pathOf(c Call) string {
	if p := detailString(c.Details, "path"); p != "" {
		return p
	}
	return argString(c.Args, "path", "file_path")
}

// Summarize picks the argument that identifies a call and renders it as one
// safe, truncated line. It never panics.
func Summarize(name string, args json.RawMessage, max int) string {
	if max <= 0 {
		max = 60
	}
	m := argMap(args)
	if len(m) == 0 {
		return truncate(clean(string(args)), max)
	}
	key, value, ok := pickArg(m)
	if !ok {
		raw, _ := json.Marshal(m)
		return truncate(clean(string(raw)), max)
	}
	value = clean(value)
	if key == "path" || key == "file_path" || strings.Contains(value, "/") {
		value = elidePath(value, max)
	}
	return truncate(value, max)
}

// pickArg returns the identifying argument: a known key, then the first string
// value, in sorted key order.
func pickArg(m map[string]any) (string, string, bool) {
	for _, k := range []string{"path", "file_path", "command", "pattern", "url", "query", "name"} {
		if v, ok := m[k].(string); ok {
			if k == "command" {
				v = firstLine(v)
			}
			return k, v, true
		}
	}
	keys := slices.Sorted(maps.Keys(m))
	for _, k := range keys {
		if v, ok := m[k].(string); ok {
			return k, v, true
		}
	}
	return "", "", false
}

// firstLine returns the first line of s.
func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

// clean strips control characters and collapses whitespace.
func clean(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t' || r == '\r' || r == ' ':
			space = true
		case unicode.IsControl(r):
			// drop
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// truncate limits s to max cells with an ellipsis.
func truncate(s string, max int) string {
	if ansi.StringWidth(s) <= max {
		return s
	}
	return ansi.Truncate(s, max, "…")
}

// elidePath keeps the last path components and elides the middle.
func elidePath(s string, max int) string {
	if ansi.StringWidth(s) <= max {
		return s
	}
	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		if cand := "…/" + strings.Join(parts[len(parts)-2:], "/"); ansi.StringWidth(cand) <= max {
			return cand
		}
	}
	base := path.Base(s)
	if cand := "…/" + base; ansi.StringWidth(cand) <= max {
		return cand
	}
	return truncate(base, max)
}

// FormatDuration renders a duration as 850ms, 4s, or 1m 05s.
func FormatDuration(d time.Duration) string {
	switch {
	case d < 0:
		return "0ms"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dm %02ds", int(d/time.Minute), int(d.Seconds())%60)
	}
}

// FormatTokens renders a token count as 640, 3.4k, 15k, or 1.2M.
func FormatTokens(n int) string {
	switch {
	case n < 0:
		return "0"
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10_000:
		if n%1000 == 0 {
			return fmt.Sprintf("%dk", n/1000)
		}
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", int(math.Round(float64(n)/1000)))
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
}
