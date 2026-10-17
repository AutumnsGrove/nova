//go:build !notui

package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
	"github.com/charmbracelet/colorprofile"
)

// fakeRenderer records every call and wraps the source in a marker so tests
// can see what reached it.
type fakeRenderer struct {
	calls  int
	widths []int
	srcs   []string
	echo   bool
}

func (f *fakeRenderer) Markdown(src string, width int) string {
	f.calls++
	f.widths = append(f.widths, width)
	f.srcs = append(f.srcs, src)
	if f.echo {
		return src
	}
	return "MD(" + src + ")"
}

func TestMarkdown_MDK01_ParagraphAndHeading(t *testing.T) {
	f := &fakeRenderer{}
	m := newMarkdown(f)
	m.setWidth(40)
	m.append("# Title\n\nbody text")
	got := m.String()
	if f.calls != 1 || f.srcs[0] != "# Title" {
		t.Fatalf("renderer calls=%d srcs=%q", f.calls, f.srcs)
	}
	if !strings.Contains(got, "MD(# Title)") || !strings.Contains(got, "body text") {
		t.Fatalf("rendered output = %q", got)
	}
}

func TestMarkdown_MDK02_BlocksSplitAtBlankLines(t *testing.T) {
	f := &fakeRenderer{}
	m := newMarkdown(f)
	m.setWidth(40)
	m.append("a\n\nb\n\nc")
	_ = m.String()
	if f.calls != 2 {
		t.Fatalf("renderer called %d times, want 2 (one per closed block)", f.calls)
	}
	if strings.Join(f.srcs, "|") != "a|b" {
		t.Fatalf("renderer saw %q", f.srcs)
	}
}

func TestMarkdown_MDK03_OpenFence(t *testing.T) {
	f := &fakeRenderer{}
	m := newMarkdown(f)
	m.setWidth(40)
	m.append("```\ncode\n\nmore")
	_ = m.String()
	if f.calls != 0 {
		t.Fatalf("open fence was sent to the renderer %d times", f.calls)
	}
}

func TestMarkdown_MDK04_FenceCloses(t *testing.T) {
	f := &fakeRenderer{}
	m := newMarkdown(f)
	m.setWidth(40)
	m.append("```\ncode\n```\n\nnext")
	_ = m.String()
	if f.calls != 1 {
		t.Fatalf("renderer called %d times, want 1", f.calls)
	}
	if f.srcs[0] != "```\ncode\n```" {
		t.Fatalf("renderer saw %q", f.srcs[0])
	}
}

func TestMarkdown_MDK05_RenderCount(t *testing.T) {
	f := &fakeRenderer{echo: true}
	m := newMarkdown(f)
	m.setWidth(40)
	const text = "para one\n\npara two\n\npara three"
	for _, r := range text {
		m.append(string(r))
	}
	_ = m.String()
	if f.calls > 3 {
		t.Fatalf("1000-delta style stream caused %d renders, want about 3", f.calls)
	}
}

func TestMarkdown_MDK06_TailUsesCheapPath(t *testing.T) {
	f := &fakeRenderer{}
	m := newMarkdown(f)
	m.setWidth(40)
	m.append("**bold** and `code`")
	got := m.String()
	if f.calls != 0 {
		t.Fatalf("open tail was sent to the renderer")
	}
	if !strings.Contains(got, "bold") || !strings.Contains(got, "code") {
		t.Fatalf("cheap render lost content: %q", got)
	}
}

func TestMarkdown_MDK07_WidthChange(t *testing.T) {
	f := &fakeRenderer{}
	m := newMarkdown(f)
	m.setWidth(10)
	m.append("a\n\nb")
	_ = m.String()
	if f.calls != 1 {
		t.Fatalf("first render calls = %d, want 1", f.calls)
	}
	m.setWidth(20)
	_ = m.String()
	if f.calls != 2 {
		t.Fatalf("width change calls = %d, want 2", f.calls)
	}
	if f.widths[1] != 20 {
		t.Fatalf("second render width = %d, want 20", f.widths[1])
	}
}

func TestMarkdown_MDK08_NeverWiderThanWidth(t *testing.T) {
	f := &fakeRenderer{echo: true}
	for _, width := range []int{4, 10, 20} {
		m := newMarkdown(f)
		m.setWidth(width)
		m.append("supercalifragilisticexpialidocious and aaaaaaaaaaaaaaaaaaaaaaa")
		for _, line := range strings.Split(m.String(), "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Fatalf("width %d: line %q is %d cells", width, line, w)
			}
		}
	}
}

func TestMarkdown_MDK09_ANSIStripped(t *testing.T) {
	f := &fakeRenderer{echo: true}
	m := newMarkdown(f)
	m.setWidth(40)
	m.append("hello \x1b[2Jworld\x1b[31mred\x1b[0m and \x1b]0;title\x07end")
	got := m.String()
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
		t.Fatalf("output contains control sequences: %q", got)
	}
}

func TestMarkdown_MDK10_CombiningMarksAndEmoji(t *testing.T) {
	f := &fakeRenderer{echo: true}
	m := newMarkdown(f)
	m.setWidth(2)
	m.append("e\u0301😀😀😀😀")
	for _, line := range strings.Split(m.String(), "\n") {
		if w := ansi.StringWidth(line); w > 2 {
			t.Fatalf("line %q is %d cells", line, w)
		}
	}
}

func TestMarkdown_MDK11_TabsExpanded(t *testing.T) {
	f := &fakeRenderer{echo: true}
	m := newMarkdown(f)
	m.setWidth(20)
	m.append("\tfoo\tbar")
	got := m.String()
	if strings.ContainsRune(got, '\t') {
		t.Fatalf("tabs were not expanded: %q", got)
	}
}

func TestMarkdown_MDK12_EmptyAndWhitespace(t *testing.T) {
	for _, src := range []string{"", "   ", "\n\n\n", "  \n\t\n"} {
		m := newMarkdown(&fakeRenderer{})
		m.setWidth(10)
		m.append(src)
		if got := m.String(); strings.TrimSpace(got) != "" && got == "" {
			t.Fatalf("unexpected output %q for %q", got, src)
		}
	}
}

func TestMarkdown_MDK13_VeryLongMessage(t *testing.T) {
	big := strings.Repeat("word ", 200_000) // about 1 MiB
	f := &fakeRenderer{echo: true}
	m := newMarkdown(f)
	m.setWidth(80)
	start := time.Now()
	m.append(big)
	_ = m.String()
	// 10s leaves room for -race overhead on a loaded runner; a real regression
	// is an order of magnitude slower than the ~0.2s this takes without
	// instrumentation.
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("1 MiB took %s", elapsed)
	}
}

func TestMarkdown_MDK14_PassThrough(t *testing.T) {
	f := &fakeRenderer{}
	m := newMarkdown(f)
	m.setWidth(40)
	m.append("- item\n\n> quote\n\n| a | b |\n\n`inline`")
	_ = m.String()
	want := []string{"- item", "> quote", "| a | b |"}
	for _, w := range want {
		found := false
		for _, s := range f.srcs {
			if s == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("block %q did not reach the renderer: %q", w, f.srcs)
		}
	}
}

// MDK15: consecutive blocks are separated by exactly one blank line. Glamour's
// document style prefixes every render with a newline, which the join must not
// turn into a second blank line.
func TestMarkdown_MDK15_SingleBlankLineBetweenBlocks(t *testing.T) {
	r := NewGlamourRenderer(styles.Build(styles.Dark(), colorprofile.TrueColor))
	m := newMarkdown(r)
	m.setWidth(60)
	m.append("# Title\n\nfirst paragraph\n\n- a\n- b\n\n```go\npackage main\n```\n\n## Second\n\nsecond paragraph")
	got := m.String()
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("blocks are separated by more than one blank line:\n%q", got)
	}
}

// TestGlamourRenderer_RendersMarkdown is a smoke test for the production
// renderer: it renders through glamour and never exceeds the width.
func TestGlamourRenderer_RendersMarkdown(t *testing.T) {
	r := NewGlamourRenderer(styles.Build(styles.Mono(), colorprofile.ASCII))
	got := r.Markdown("# Heading\n\nsome text", 40)
	if !strings.Contains(got, "Heading") {
		t.Fatalf("glamour output = %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Fatalf("glamour line %q exceeds width", line)
		}
	}
}

// FuzzMarkdownIncremental asserts that any chunking of the input produces the
// same output as appending the whole text at once.
func FuzzMarkdownIncremental(f *testing.F) {
	f.Add("hello\n\nworld", uint8(3))
	f.Add("```go\ncode\n```\n\ntext", uint8(1))
	f.Fuzz(func(t *testing.T, text string, seed uint8) {
		whole := newMarkdown(&fakeRenderer{echo: true})
		whole.setWidth(20)
		whole.append(text)

		inc := newMarkdown(&fakeRenderer{echo: true})
		inc.setWidth(20)
		step := int(seed)%5 + 1
		for i := 0; i < len(text); i += step {
			end := i + step
			if end > len(text) {
				end = len(text)
			}
			inc.append(text[i:end])
		}
		if got, want := inc.String(), whole.String(); got != want {
			t.Fatalf("incremental output differs:\n got %q\nwant %q", got, want)
		}
	})
}
