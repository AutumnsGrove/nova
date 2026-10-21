package webfetch

import (
	"strings"
	"testing"
)

func TestWebfetch_WFT39_PreKeepsLinesAndIndent(t *testing.T) {
	got := htmlToMarkdown([]byte("<pre><code>func main() {\n\tprintln(1)\n}</code></pre>"), nil)
	want := "```\nfunc main() {\n\tprintln(1)\n}\n```"
	if got != want {
		t.Fatalf("markdown = %q, want %q", got, want)
	}
	// A code block inside a list item or quote must keep its lines too.
	got = htmlToMarkdown([]byte("<blockquote><pre>a\nb</pre></blockquote>"), nil)
	if !strings.Contains(got, "> a\n> b") {
		t.Fatalf("quoted pre = %q", got)
	}
}

func TestWebfetch_WFT40_BlankElementsDoNotIndent(t *testing.T) {
	doc := "<p>a</p>" + strings.Repeat("<div> </div>", 6) + "<p>b</p>"
	if got, want := htmlToMarkdown([]byte(doc), nil), "a\n\nb"; got != want {
		t.Fatalf("markdown = %q, want %q", got, want)
	}
}

func TestWebfetch_WFT41_BlankLinesCollapse(t *testing.T) {
	got := htmlToMarkdown([]byte("<p>a<br> <br> <br> <br>b</p>"), nil)
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("three newlines survived: %q", got)
	}
}

func TestWebfetch_WFT42_UnsafeLinkTargets(t *testing.T) {
	doc := `<a href="JavaScript:alert(1)">x</a> <a href=" vbscript:y">z</a> <img alt="pic" src="data:image/png;base64,AAAA">`
	got := htmlToMarkdown([]byte(doc), nil)
	for _, bad := range []string{"alert", "vbscript", "base64"} {
		if strings.Contains(got, bad) {
			t.Fatalf("markdown kept %q: %q", bad, got)
		}
	}
	if !strings.Contains(got, "x") || !strings.Contains(got, "z") {
		t.Fatalf("link text was lost: %q", got)
	}
}
