package webfetch

import (
	"bytes"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// blockBoilerplate are the elements dropped when a page has a main region.
var blockBoilerplate = map[string]bool{"nav": true, "footer": true, "aside": true}

// dropElements are always dropped.
var dropElements = map[string]bool{"script": true, "style": true, "template": true, "noscript": true, "svg": true, "head": true}

// htmlToMarkdown converts an HTML document to markdown. Relative URLs in links
// and images resolve against base.
func htmlToMarkdown(body []byte, base *url.URL) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return string(body)
	}
	c := &converter{base: base, hasMain: hasElement(doc, "main", "article")}
	c.walk(doc, 0, 0)
	return collapseBlank(normalize(c.b.String()))
}

// hasElement reports whether the tree contains any of the named elements.
func hasElement(n *html.Node, names ...string) bool {
	if n.Type == html.ElementNode {
		for _, name := range names {
			if n.Data == name {
				return true
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if hasElement(c, names...) {
			return true
		}
	}
	return false
}

// converter carries the output buffer and a little nesting state.
type converter struct {
	b       strings.Builder
	base    *url.URL
	hasMain bool
}

// walk renders one node. listDepth is the current list nesting, quoteDepth the
// block-quote nesting (used for the "> " prefix).
func (c *converter) walk(n *html.Node, listDepth, quoteDepth int) {
	switch n.Type {
	case html.TextNode:
		c.text(n.Data)
		return
	case html.CommentNode, html.DoctypeNode:
		return
	case html.ElementNode:
	default:
		c.children(n, listDepth, quoteDepth)
		return
	}
	switch {
	case dropElements[n.Data]:
		return
	case c.hasMain && blockBoilerplate[n.Data]:
		return
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		c.block()
		c.b.WriteString(strings.Repeat("#", int(n.Data[1]-'0')) + " ")
		c.children(n, listDepth, quoteDepth)
		c.block()
	case "p", "div", "section", "article", "main":
		c.block()
		c.children(n, listDepth, quoteDepth)
		c.block()
	case "br":
		c.b.WriteString("\n")
	case "hr":
		c.block()
		c.b.WriteString("---")
		c.block()
	case "strong", "b":
		c.b.WriteString("**")
		c.children(n, listDepth, quoteDepth)
		c.b.WriteString("**")
	case "em", "i":
		c.b.WriteString("*")
		c.children(n, listDepth, quoteDepth)
		c.b.WriteString("*")
	case "a":
		c.link(n, listDepth, quoteDepth)
	case "img":
		c.image(n)
	case "code":
		if n.Parent != nil && n.Parent.Data == "pre" {
			c.children(n, listDepth, quoteDepth)
			return
		}
		c.b.WriteString("`")
		c.children(n, listDepth, quoteDepth)
		c.b.WriteString("`")
	case "pre":
		c.block()
		c.b.WriteString("```\n")
		c.children(n, listDepth, quoteDepth)
		if !strings.HasSuffix(c.b.String(), "\n") {
			c.b.WriteString("\n")
		}
		c.b.WriteString("```")
		c.block()
	case "ul", "ol":
		c.block()
		c.list(n, listDepth, n.Data == "ol")
		c.block()
	case "blockquote":
		text := c.subtree(n, listDepth, quoteDepth)
		c.block()
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			c.b.WriteString("> " + line + "\n")
		}
	case "table":
		c.block()
		c.table(n)
		c.block()
	default:
		c.children(n, listDepth, quoteDepth)
	}
}

// children walks every child.
func (c *converter) children(n *html.Node, listDepth, quoteDepth int) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.walk(ch, listDepth, quoteDepth)
	}
}

// subtree renders a node's children into a fresh converter and returns it.
func (c *converter) subtree(n *html.Node, listDepth, quoteDepth int) string {
	inner := &converter{base: c.base, hasMain: c.hasMain}
	inner.children(n, listDepth, quoteDepth)
	return inner.b.String()
}

// link renders an anchor, resolving a relative href.
func (c *converter) link(n *html.Node, listDepth, quoteDepth int) {
	href := attr(n, "href")
	text := strings.TrimSpace(c.subtree(n, listDepth, quoteDepth))
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") {
		c.b.WriteString(text)
		return
	}
	c.b.WriteString("[" + text + "](" + c.resolve(href) + ")")
}

// image renders an image, resolving a relative src.
func (c *converter) image(n *html.Node) {
	src := attr(n, "src")
	if src == "" {
		return
	}
	c.b.WriteString("![" + attr(n, "alt") + "](" + c.resolve(src) + ")")
}

// resolve turns a relative URL into an absolute one against the base.
func (c *converter) resolve(ref string) string {
	if c.base == nil {
		return ref
	}
	u, err := c.base.Parse(ref)
	if err != nil {
		return ref
	}
	return u.String()
}

// list renders a ul or ol.
func (c *converter) list(n *html.Node, depth int, ordered bool) {
	index := 1
	for li := n.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.Data != "li" {
			continue
		}
		c.b.WriteString(strings.Repeat("  ", depth))
		if ordered {
			c.b.WriteString(strconv.Itoa(index) + ". ")
			index++
		} else {
			c.b.WriteString("- ")
		}
		// Render the item, then nested lists on following lines.
		inner := &converter{base: c.base, hasMain: c.hasMain}
		inner.children(li, depth+1, 0)
		c.b.WriteString(strings.TrimSpace(inner.b.String()))
		c.b.WriteString("\n")
	}
}

// table renders a simple HTML table as a markdown table.
func (c *converter) table(n *html.Node) {
	var rows [][]string
	var walkRows func(*html.Node)
	walkRows = func(node *html.Node) {
		for ch := node.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type != html.ElementNode {
				continue
			}
			switch ch.Data {
			case "tr":
				rows = append(rows, rowCells(ch))
			case "thead", "tbody", "tfoot":
				walkRows(ch)
			}
		}
	}
	walkRows(n)
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	for i, r := range rows {
		cells := make([]string, width)
		for j := range cells {
			if j < len(r) {
				cells[j] = r[j]
			}
		}
		c.b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		if i == 0 {
			sep := make([]string, width)
			for j := range sep {
				sep[j] = "---"
			}
			c.b.WriteString("| " + strings.Join(sep, " | ") + " |\n")
		}
	}
}

// rowCells returns a row's trimmed cell text.
func rowCells(tr *html.Node) []string {
	var out []string
	for td := tr.FirstChild; td != nil; td = td.NextSibling {
		if td.Type != html.ElementNode || (td.Data != "td" && td.Data != "th") {
			continue
		}
		out = append(out, strings.TrimSpace(textOf(td)))
	}
	return out
}

// textOf collects concatenated text.
func textOf(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for ch := node.FirstChild; ch != nil; ch = ch.NextSibling {
			rec(ch)
		}
	}
	rec(n)
	return b.String()
}

// text writes collapsed text: runs of whitespace become one space, and a
// leading or trailing space in the source is preserved so inline runs do not
// run together.
func (c *converter) text(s string) {
	if strings.TrimSpace(s) == "" {
		c.b.WriteString(" ")
		return
	}
	lead := strings.TrimLeft(s, " \t\r\n") != s
	trail := strings.TrimRight(s, " \t\r\n") != s
	collapsed := strings.Join(strings.Fields(s), " ")
	if lead {
		c.b.WriteString(" ")
	}
	c.b.WriteString(collapsed)
	if trail {
		c.b.WriteString(" ")
	}
}

// block ensures a blank line separates block elements.
func (c *converter) block() {
	cur := c.b.String()
	cur = strings.TrimRight(cur, " ")
	if cur == "" {
		return
	}
	if !strings.HasSuffix(cur, "\n\n") {
		if strings.HasSuffix(cur, "\n") {
			c.b.WriteString("\n")
		} else {
			c.b.WriteString("\n\n")
		}
	}
}

// attr returns an element attribute.
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// collapseBlank removes trailing spaces on each line and leading/trailing blank
// lines.
func collapseBlank(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	out := strings.Join(lines, "\n")
	return strings.TrimSpace(out + "\n")
}

// multipleSpaces collapses runs of three or more newlines to two.
var multipleSpaces = regexp.MustCompile(`\n{3,}`)

// normalize softens excessive blank lines.
func normalize(s string) string {
	return multipleSpaces.ReplaceAllString(s, "\n\n")
}

// windows1252 maps the C1 range to the glyphs Windows-1252 uses there.
var windows1252 = [32]rune{
	'€', '\u0081', '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', '\u008d', 'Ž', '\u008f',
	'\u0090', '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', '\u009d', 'ž', 'Ÿ',
}

// charsetOf finds the charset from the header, then a meta tag.
func charsetOf(body []byte, header string) string {
	if _, params, err := mime.ParseMediaType(header); err == nil {
		if cs := strings.TrimSpace(params["charset"]); cs != "" {
			return strings.ToLower(cs)
		}
	}
	head := body
	if len(head) > 2048 {
		head = head[:2048]
	}
	s := strings.ToLower(string(head))
	if i := strings.Index(s, "charset="); i >= 0 {
		rest := s[i+len("charset="):]
		rest = strings.TrimLeft(rest, "\"' ")
		if end := strings.IndexAny(rest, "\"'; \t\r\n/>"); end >= 0 {
			rest = rest[:end]
		}
		if rest != "" {
			return rest
		}
	}
	return "utf-8"
}

// decodeCharset decodes a body to UTF-8, replacing invalid bytes with U+FFFD.
func decodeCharset(body []byte, charset string) string {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return strings.ToValidUTF8(string(body), "\uFFFD")
	case "iso-8859-1", "latin1", "latin-1", "iso8859-1", "cp819":
		var b strings.Builder
		for _, c := range body {
			b.WriteRune(rune(c))
		}
		return b.String()
	case "windows-1252", "cp1252", "x-cp1252":
		var b strings.Builder
		for _, c := range body {
			if c >= 0x80 && c <= 0x9F {
				b.WriteRune(windows1252[c-0x80])
			} else {
				b.WriteRune(rune(c))
			}
		}
		return b.String()
	default:
		return strings.ToValidUTF8(string(body), "\uFFFD")
	}
}
