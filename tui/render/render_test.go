//go:build !notui

package render

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

func mono() styles.Styles   { return styles.Build(styles.Mono(), colorprofile.ASCII) }
func colors() styles.Styles { return styles.Build(styles.Dark(), colorprofile.TrueColor) }

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestRender_ARG01_PicksPath(t *testing.T) {
	if got := Summarize("read", raw(`{"path":"a.go"}`), 60); got != "a.go" {
		t.Fatalf("Summarize = %q", got)
	}
}

func TestRender_ARG02_CommandFirstLine(t *testing.T) {
	got := Summarize("bash", raw(`{"command":"ls -la\nrm -rf /"}`), 60)
	if got != "ls -la" {
		t.Fatalf("Summarize = %q", got)
	}
}

func TestRender_ARG03_PatternURLQuery(t *testing.T) {
	cases := map[string]string{
		`{"pattern":"TODO"}`:      "TODO",
		`{"url":"https://x.dev"}`: "https://x.dev",
		`{"query":"hello"}`:       "hello",
	}
	for in, want := range cases {
		if got := Summarize("t", raw(in), 60); got != want {
			t.Fatalf("Summarize(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestRender_ARG04_Fallback(t *testing.T) {
	if got := Summarize("t", raw(`{"zzz":"hello"}`), 60); got != "hello" {
		t.Fatalf("first string = %q", got)
	}
	if got := Summarize("t", raw(`{"n":1}`), 60); got != `{"n":1}` {
		t.Fatalf("compact JSON = %q", got)
	}
}

func TestRender_ARG05_LongPathElision(t *testing.T) {
	got := Summarize("read", raw(`{"path":"/very/deep/dir/file.go"}`), 15)
	if !strings.HasSuffix(got, "file.go") || ansi.StringWidth(got) > 15 {
		t.Fatalf("elided path = %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("expected an ellipsis in %q", got)
	}
}

func TestRender_ARG06_ControlAndWhitespace(t *testing.T) {
	got := Summarize("read", raw(`{"path":"a\tb\nc"}`), 60)
	if got != "a b c" {
		t.Fatalf("cleaned = %q", got)
	}
}

func TestRender_ARG07_Truncation(t *testing.T) {
	got := Summarize("t", raw(`{"name":"`+strings.Repeat("x", 100)+`"}`), 10)
	if ansi.StringWidth(got) > 10 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated = %q", got)
	}
}

func TestRender_ARG08_InvalidJSON(t *testing.T) {
	if got := Summarize("t", raw(`{not json`), 40); got == "" {
		t.Fatal("invalid JSON gave an empty summary")
	}
}

func call(name, args string) Call {
	return Call{Name: name, Args: raw(args)}
}

func TestRender_CRD01_HeaderOneLine(t *testing.T) {
	st := mono()
	r := NewRegistry()
	c := call("read", `{"path":"`+strings.Repeat("x", 200)+`"}`)
	h := r.Lookup("read").Header(c, st, 30)
	if strings.Contains(h, "\n") || ansi.StringWidth(h) > 30 {
		t.Fatalf("header = %q", h)
	}
}

func TestRender_CRD02_StatusDots(t *testing.T) {
	st := mono()
	r := NewRegistry().Lookup("read")
	if h := r.Header(Call{Name: "read", Running: true}, st, 40); !strings.Contains(h, "⠋") {
		t.Fatalf("running dot missing: %q", h)
	}
	if h := r.Header(Call{Name: "read"}, st, 40); !strings.Contains(h, "●") {
		t.Fatalf("ok dot missing: %q", h)
	}
	if h := r.Header(Call{Name: "read", IsError: true}, st, 40); !strings.Contains(h, "✖") {
		t.Fatalf("error dot missing: %q", h)
	}
}

func TestRender_CRD03_SpinnerAnimates(t *testing.T) {
	st := mono()
	r := NewRegistry().Lookup("read")
	t0 := time.Unix(0, 0)
	a := r.Header(Call{Name: "read", Running: true, Started: t0, Now: t0.Add(100 * time.Millisecond)}, st, 40)
	b := r.Header(Call{Name: "read", Running: true, Started: t0, Now: t0.Add(200 * time.Millisecond)}, st, 40)
	if a == b {
		t.Fatalf("spinner did not advance: %q", a)
	}
}

func TestRender_CRD04_DurationDroppedFirst(t *testing.T) {
	st := mono()
	t0 := time.Unix(0, 0)
	c := Call{Name: "read", Args: raw(`{"path":"a.go"}`), Started: t0, Ended: t0.Add(2 * time.Second)}
	h := NewRegistry().Lookup("read").Header(c, st, 12)
	if strings.Contains(h, "2s") {
		t.Fatalf("duration not dropped: %q", h)
	}
}

// CRD05 once asserted that a finished call hides its body. Issue #5 changed
// that on purpose: a finished call shows a short preview (see TCP01), and only
// a call with no result has nothing to show.
func TestRender_CRD05_NoResultNoBody(t *testing.T) {
	st := mono()
	c := Call{Name: "read", Args: raw(`{"path":"a.go"}`)}
	if got := NewRegistry().Lookup("read").Body(c, st, 40, false); got != "" {
		t.Fatalf("body = %q, want empty", got)
	}
}

func TestRender_CRD06_RunningBodyTail(t *testing.T) {
	st := mono()
	c := Call{Name: "generic", Running: true, Live: "1\n2\n3\n4\n5\n6\n7"}
	got := NewRegistry().Lookup("generic").Body(c, st, 40, false)
	if !strings.Contains(got, "7") || strings.Contains(got, "1\n") {
		t.Fatalf("running tail = %q", got)
	}
}

func TestRender_CRD07_ErrorBody(t *testing.T) {
	st := mono()
	c := Call{Name: "t", IsError: true, Content: "boom"}
	if got := NewRegistry().Lookup("t").Body(c, st, 40, false); !strings.Contains(got, "boom") {
		t.Fatalf("error body = %q", got)
	}
}

func TestRender_CRD08_ExpandedBody(t *testing.T) {
	st := mono()
	c := Call{Name: "t", Content: "result"}
	if got := NewRegistry().Lookup("t").Body(c, st, 40, true); !strings.Contains(got, "result") {
		t.Fatalf("expanded body = %q", got)
	}
}

func TestRender_CRD09_GenericFallback(t *testing.T) {
	st := mono()
	got := NewRegistry().Lookup("mystery").Header(call("mystery", `{"path":"a"}`), st, 40)
	if !strings.Contains(got, "mystery") || !strings.Contains(got, "a") {
		t.Fatalf("generic header = %q", got)
	}
}

// panicRenderer panics in both methods.
type panicRenderer struct{}

func (panicRenderer) Header(Call, styles.Styles, int) string     { panic("header") }
func (panicRenderer) Body(Call, styles.Styles, int, bool) string { panic("body") }

func TestRender_CRD10_RendererPanic(t *testing.T) {
	st := mono()
	r := NewRegistry()
	r.Register("boom", panicRenderer{})
	got := r.Lookup("boom").Header(call("boom", `{"path":"a"}`), st, 60)
	if !strings.Contains(got, "renderer error") {
		t.Fatalf("panic not surfaced: %q", got)
	}
	if body := r.Lookup("boom").Body(call("boom", `{}`), st, 60, true); body == "" {
		t.Fatal("panic body did not fall back")
	}
}

func TestRender_CRD12_NoControlSequences(t *testing.T) {
	st := mono()
	c := Call{Name: "t", Content: "a\x1b[2Jb\x1b]0;title\x07c"}
	got := NewRegistry().Lookup("t").Body(c, st, 80, true)
	if strings.Contains(got, "\x1b[2J") || strings.ContainsRune(got, '\x07') {
		t.Fatalf("control sequences survived: %q", got)
	}
}

func TestRender_CRD14_TruncatedMarker(t *testing.T) {
	st := mono()
	got := NewRegistry().Lookup("t").Header(Call{Name: "t", Truncated: true}, st, 80)
	if !strings.Contains(got, "(truncated)") {
		t.Fatalf("truncated marker missing: %q", got)
	}
}

func TestRender_RRD01_ReadRange(t *testing.T) {
	st := mono()
	c := Call{Name: "read", Details: raw(`{"start":1,"end":3}`)}
	if got := NewRegistry().Lookup("read").Header(c, st, 60); !strings.Contains(got, "[1–3]") {
		t.Fatalf("read header = %q", got)
	}
}

func TestRender_RRD02_ReadBodyGutter(t *testing.T) {
	st := mono()
	c := Call{Name: "read", Args: raw(`{"path":"a.go"}`), Content: "1\tpackage main\n2\tfunc main() {}"}
	got := NewRegistry().Lookup("read").Body(c, st, 60, true)
	if !strings.Contains(got, "1") || !strings.Contains(got, "package main") {
		t.Fatalf("read body = %q", got)
	}
}

func TestRender_RRD03_ReadCapped(t *testing.T) {
	st := mono()
	var b strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "%d\tline\n", i+1)
	}
	c := Call{Name: "read", Content: b.String()}
	got := NewRegistry().Lookup("read").Body(c, st, 60, true)
	if !strings.Contains(got, "300 more lines") {
		t.Fatalf("read cap missing:\n%s", got)
	}
}

func TestRender_RRD04_UnknownExtension(t *testing.T) {
	st := mono()
	c := Call{Name: "read", Content: "1\tplain"}
	if got := NewRegistry().Lookup("read").Body(c, st, 60, true); !strings.Contains(got, "plain") {
		t.Fatalf("body = %q", got)
	}
}

func TestRender_RRD05_ErrorResult(t *testing.T) {
	st := mono()
	c := Call{Name: "read", IsError: true, Content: "not found"}
	if got := NewRegistry().Lookup("read").Body(c, st, 60, false); !strings.Contains(got, "not found") {
		t.Fatalf("error body = %q", got)
	}
}

func TestRender_RWR01_WriteHeader(t *testing.T) {
	st := mono()
	r := NewRegistry().Lookup("write")
	created := r.Header(Call{Name: "write", Args: raw(`{"path":"a"}`), Details: raw(`{"created":true,"bytes":12}`)}, st, 80)
	if !strings.Contains(created, "created") || !strings.Contains(created, "12 bytes") {
		t.Fatalf("created header = %q", created)
	}
	over := r.Header(Call{Name: "write", Args: raw(`{"path":"a"}`), Details: raw(`{"created":false,"bytes":5}`)}, st, 80)
	if !strings.Contains(over, "overwrote") {
		t.Fatalf("overwrote header = %q", over)
	}
}

func TestRender_RWR02_WriteBody(t *testing.T) {
	st := mono()
	c := Call{Name: "write", Args: raw(`{"path":"a.go","content":"package main"}`)}
	if got := NewRegistry().Lookup("write").Body(c, st, 60, true); !strings.Contains(got, "package main") {
		t.Fatalf("write body = %q", got)
	}
}

func TestRender_RED01_EditHeader(t *testing.T) {
	st := mono()
	c := Call{Name: "edit", Args: raw(`{"path":"a.go"}`), Details: raw(`{"added":9,"removed":2}`)}
	got := NewRegistry().Lookup("edit").Header(c, st, 80)
	if !strings.Contains(got, "+9") || !strings.Contains(got, "−2") {
		t.Fatalf("edit header = %q", got)
	}
}

const editDiff = "@@ -1,2 +1,2 @@\n a\n-b\n+c\n" + "+d\n+e\n+f\n+g\n+h\n+i\n+j\n+k\n+l\n+m\n+n\n+o\n"

func TestRender_RED02_FreshEditCollapsed(t *testing.T) {
	st := mono()
	c := Call{Name: "edit", Args: raw(`{"path":"a.go"}`), Details: raw(`{"added":9,"removed":2,"diff":` + jsonString(editDiff) + `}`)}
	got := NewRegistry().Lookup("edit").Body(c, st, 80, false)
	if !strings.Contains(got, "more lines") {
		t.Fatalf("fresh edit not capped:\n%s", got)
	}
}

func TestRender_RED03_ExpandedEdit(t *testing.T) {
	st := mono()
	c := Call{Name: "edit", Args: raw(`{"path":"a.go"}`), Details: raw(`{"diff":` + jsonString(editDiff) + `}`)}
	got := NewRegistry().Lookup("edit").Body(c, st, 80, true)
	if !strings.Contains(got, "+o") {
		t.Fatalf("expanded edit missing the tail:\n%s", got)
	}
}

func TestRender_RED04_DiffOmitted(t *testing.T) {
	st := mono()
	c := Call{Name: "edit", Args: raw(`{"path":"a.go","edits":[{"oldText":"old","newText":"new"}]}`), Details: raw(`{"diff_omitted":true}`)}
	got := NewRegistry().Lookup("edit").Body(c, st, 80, true)
	if !strings.Contains(got, "old") || !strings.Contains(got, "new") {
		t.Fatalf("omitted body = %q", got)
	}
}

func TestRender_RED05_EditError(t *testing.T) {
	st := mono()
	c := Call{Name: "edit", IsError: true, Content: "no match"}
	if got := NewRegistry().Lookup("edit").Body(c, st, 80, false); !strings.Contains(got, "no match") {
		t.Fatalf("edit error body = %q", got)
	}
}

func TestRender_RBS01_BashHeader(t *testing.T) {
	st := mono()
	got := NewRegistry().Lookup("bash").Header(call("bash", `{"command":"ls -la"}`), st, 80)
	if !strings.Contains(got, "$ ls -la") {
		t.Fatalf("bash header = %q", got)
	}
}

func TestRender_RBS02_MultiLineCommand(t *testing.T) {
	st := mono()
	got := NewRegistry().Lookup("bash").Header(call("bash", `{"command":"a\nb"}`), st, 80)
	if !strings.Contains(got, "…") || strings.Contains(got, "b") {
		t.Fatalf("multi-line header = %q", got)
	}
}

func TestRender_RBS03_NonZeroExit(t *testing.T) {
	st := mono()
	c := Call{Name: "bash", Args: raw(`{"command":"x"}`), Details: raw(`{"exit_code":2}`)}
	if got := NewRegistry().Lookup("bash").Header(c, st, 80); !strings.Contains(got, "exit 2") {
		t.Fatalf("exit header = %q", got)
	}
}

func TestRender_RBS04_RunningTail(t *testing.T) {
	st := mono()
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	c := Call{Name: "bash", Running: true, Live: b.String()}
	got := NewRegistry().Lookup("bash").Body(c, st, 80, false)
	if !strings.Contains(got, "line19") || strings.Contains(got, "line0\n") {
		t.Fatalf("running tail = %q", got)
	}
}

func TestRender_RBS05_Expanded(t *testing.T) {
	st := mono()
	c := Call{Name: "bash", Args: raw(`{"command":"ls"}`), Content: "file1\nfile2", Details: raw(`{"exit_code":0}`)}
	got := NewRegistry().Lookup("bash").Body(c, st, 80, true)
	for _, want := range []string{"ls", "file1", "exit 0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded bash missing %q:\n%s", want, got)
		}
	}
}

func TestRender_RBS06_EscapesAndCR(t *testing.T) {
	st := mono()
	c := Call{Name: "bash", Args: raw(`{"command":"x"}`), Content: "a\rb\x1b[2Jc"}
	got := NewRegistry().Lookup("bash").Body(c, st, 80, true)
	if strings.ContainsAny(got, "\r") || strings.Contains(got, "\x1b[2J") {
		t.Fatalf("dirty output: %q", got)
	}
}

func TestRender_RBS07_NoOutput(t *testing.T) {
	st := mono()
	c := Call{Name: "bash", Args: raw(`{"command":"true"}`)}
	if got := NewRegistry().Lookup("bash").Body(c, st, 80, true); !strings.Contains(got, "(no output)") {
		t.Fatalf("no-output body = %q", got)
	}
}

func TestRender_RSR01_GrepHeader(t *testing.T) {
	st := mono()
	c := Call{Name: "grep", Args: raw(`{"pattern":"TODO"}`), Details: raw(`{"matches":12,"files":3}`)}
	if got := NewRegistry().Lookup("grep").Header(c, st, 80); !strings.Contains(got, "12 matches") {
		t.Fatalf("grep header = %q", got)
	}
}

func TestRender_RSR02_CountFromContent(t *testing.T) {
	st := mono()
	c := Call{Name: "glob", Args: raw(`{"pattern":"*.go"}`), Content: "a.go\nb.go"}
	if got := NewRegistry().Lookup("glob").Header(c, st, 80); !strings.Contains(got, "2 files") {
		t.Fatalf("glob header = %q", got)
	}
}

func TestRender_RSR03_MatchHighlight(t *testing.T) {
	st := colors()
	c := Call{Name: "grep", Args: raw(`{"pattern":"foo"}`), Content: "a foo b"}
	got := NewRegistry().Lookup("grep").Body(c, st, 80, true)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("match not highlighted: %q", got)
	}
}

func TestRender_RSR04_GlobAndList(t *testing.T) {
	st := mono()
	g := Call{Name: "glob", Args: raw(`{"pattern":"*.go"}`), Details: raw(`{"count":4}`)}
	if got := NewRegistry().Lookup("glob").Header(g, st, 80); !strings.Contains(got, "4 files") {
		t.Fatalf("glob = %q", got)
	}
	l := Call{Name: "list", Args: raw(`{"path":"src"}`), Details: raw(`{"count":2}`)}
	if got := NewRegistry().Lookup("list").Header(l, st, 80); !strings.Contains(got, "2 files") {
		t.Fatalf("list = %q", got)
	}
}

func TestRender_RGN01_GenericBody(t *testing.T) {
	st := mono()
	c := Call{Name: "t", Args: raw(`{"a":"b"}`), Content: "result"}
	got := NewRegistry().Lookup("t").Body(c, st, 80, true)
	if !strings.Contains(got, `"a": "b"`) || !strings.Contains(got, "result") {
		t.Fatalf("generic body = %q", got)
	}
}

func TestRender_RGN02_InvalidArgs(t *testing.T) {
	st := mono()
	c := Call{Name: "t", Args: raw(`{not json`), Content: "r"}
	if got := NewRegistry().Lookup("t").Body(c, st, 80, true); !strings.Contains(got, "r") {
		t.Fatalf("generic body = %q", got)
	}
}

// jsonString encodes s as a JSON string literal for building Details.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// FuzzSummarize asserts Summarize never panics and never exceeds max.
func FuzzSummarize(f *testing.F) {
	f.Add(`{"path":"a.go"}`, 20)
	f.Add(`{not json`, 5)
	f.Add("", 0)
	f.Fuzz(func(t *testing.T, args string, max int) {
		if max < 1 {
			max = 1
		}
		got := Summarize("t", json.RawMessage(args), max)
		if w := ansi.StringWidth(got); w > max {
			t.Fatalf("Summary %q is %d wide, max %d", got, w, max)
		}
	})
}

// TestProperty_CRD_NoOverflow asserts that for random calls every rendered line
// fits the width and that injected control sequences never survive.
func TestProperty_CRD_NoOverflow(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	names := []string{"read", "write", "edit", "bash", "glob", "grep", "list", "mystery"}
	reg := NewRegistry()
	st := colors()
	for i := 0; i < 3000; i++ {
		width := rng.Intn(200) + 1
		c := Call{
			Name:      names[rng.Intn(len(names))],
			Args:      raw(fmt.Sprintf(`{"path":"p%d.go","content":"%s","command":"c%d"}`, i, "\x1b[2Jx", i)),
			Content:   fmt.Sprintf("result \x1b[2J %d\nsecond", i),
			Live:      "live\noutput",
			Running:   rng.Intn(2) == 0,
			IsError:   rng.Intn(4) == 0,
			Truncated: rng.Intn(4) == 0,
			Details:   raw(`{"added":3,"removed":1,"exit_code":2,"matches":4,"count":5,"start":1,"end":9}`),
		}
		rd := reg.Lookup(c.Name)
		for _, out := range []string{rd.Header(c, st, width), rd.Body(c, st, width, rng.Intn(2) == 0)} {
			for _, l := range strings.Split(out, "\n") {
				if w := ansi.StringWidth(l); w > width {
					t.Fatalf("width %d: %q is %d wide", width, l, w)
				}
				if strings.Contains(l, "\x1b[2J") {
					t.Fatalf("injected sequence survived: %q", l)
				}
			}
		}
	}
}

func TestRender_CRD11_WidthNeverExceeded(t *testing.T) {
	st := colors()
	reg := NewRegistry()
	for width := 20; width <= 200; width += 30 {
		c := Call{
			Name:    "read",
			Args:    raw(`{"path":"世界/very/long/path.go"}`),
			Content: "1\t世界世界世界",
			Details: raw(`{"start":1,"end":1}`),
		}
		for _, out := range []string{reg.Lookup("read").Header(c, st, width), reg.Lookup("read").Body(c, st, width, true)} {
			for _, l := range strings.Split(out, "\n") {
				if ansi.StringWidth(l) > width {
					t.Fatalf("width %d: %q is %d wide", width, l, ansi.StringWidth(l))
				}
			}
		}
	}
}

// numbered returns n lines "L1".."Ln" (with a read-style gutter when numbered).
func numbered(n int, gutter bool) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if gutter {
			fmt.Fprintf(&b, "%d\tL%d\n", i, i)
		} else {
			fmt.Fprintf(&b, "L%d\n", i)
		}
	}
	return b.String()
}

// previewCalls are finished calls with a long result, one per renderer. The
// bash result is read from the tail, the rest from the head.
func previewCalls() map[string]Call {
	return map[string]Call{
		"bash":    {Name: "bash", Args: raw(`{"command":"ls"}`), Content: numbered(20, false), Details: raw(`{"exit_code":0}`)},
		"read":    {Name: "read", Args: raw(`{"path":"a.txt"}`), Content: numbered(20, true)},
		"grep":    {Name: "grep", Args: raw(`{"pattern":"zzz"}`), Content: numbered(20, false)},
		"write":   {Name: "write", Args: raw(`{"path":"a.txt","content":` + jsonString(numbered(20, false)) + `}`)},
		"generic": {Name: "other", Content: numbered(20, false)},
		"edit":    {Name: "edit", Args: raw(`{"path":"a.go"}`), Details: raw(`{"diff":` + jsonString(editDiff) + `}`)},
	}
}

func TestRender_TCP01_CollapsedShowsSixLinePreview(t *testing.T) {
	st := mono()
	for name, c := range previewCalls() {
		got := ansi.Strip(NewRegistry().Lookup(c.Name).Body(c, st, 80, false))
		if got == "" {
			t.Fatalf("%s: finished call hides its result", name)
		}
		lines := strings.Split(got, "\n")
		if len(lines) > PreviewLines+1 {
			t.Fatalf("%s: %d lines, want at most %d plus a hint:\n%s", name, len(lines), PreviewLines, got)
		}
		if !strings.Contains(got, "click to expand") {
			t.Fatalf("%s: no expand hint:\n%s", name, got)
		}
	}
}

func TestRender_TCP02_PreviewDirection(t *testing.T) {
	st := mono()
	calls := previewCalls()
	bash := ansi.Strip(NewRegistry().Lookup("bash").Body(calls["bash"], st, 80, false))
	if !strings.Contains(bash, "L20") || strings.Contains(bash, "L1\n") {
		t.Fatalf("bash preview should show the tail:\n%s", bash)
	}
	grep := ansi.Strip(NewRegistry().Lookup("grep").Body(calls["grep"], st, 80, false))
	if !strings.Contains(grep, "L1") || strings.Contains(grep, "L20") {
		t.Fatalf("grep preview should show the head:\n%s", grep)
	}
}

func TestRender_TCP03_ShortResultShownWhole(t *testing.T) {
	st := mono()
	c := Call{Name: "bash", Args: raw(`{"command":"ls"}`), Content: numbered(3, false)}
	got := ansi.Strip(NewRegistry().Lookup("bash").Body(c, st, 80, false))
	if !strings.Contains(got, "L1") || !strings.Contains(got, "L3") || strings.Contains(got, "expand") {
		t.Fatalf("short result:\n%s", got)
	}
}

func TestRender_TCP04_NoOutputStaysHeaderOnly(t *testing.T) {
	c := Call{Name: "bash", Args: raw(`{"command":"true"}`)}
	if got := NewRegistry().Lookup("bash").Body(c, mono(), 80, false); got != "" {
		t.Fatalf("empty bash output drew a body: %q", got)
	}
}

func TestRender_TCP05_ExpandedStillFull(t *testing.T) {
	st := mono()
	for name, c := range previewCalls() {
		got := ansi.Strip(NewRegistry().Lookup(c.Name).Body(c, st, 80, true))
		if strings.Contains(got, "click to expand") {
			t.Fatalf("%s: expanded view still offers to expand:\n%s", name, got)
		}
		if name != "edit" && !strings.Contains(got, "L20") {
			t.Fatalf("%s: expanded view lost its tail:\n%s", name, got)
		}
	}
}
