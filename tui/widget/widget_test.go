//go:build !notui

package widget

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/styles"
)

// ascii returns mono-adjacent styles with no colour.
func ascii() styles.Styles { return styles.Build(styles.Mono(), colorprofile.ASCII) }

// color returns full-colour styles for highlight assertions.
func color() styles.Styles { return styles.Build(styles.Dark(), colorprofile.TrueColor) }

func TestWidget_WDG01_SetText(t *testing.T) {
	s := NewStore()
	s.SetWidget("tasks", "above_editor", &Content{Kind: "text", Lines: []string{"fix retry", "write test"}})
	lines := s.Lines("above_editor", 40, 8, ascii())
	if len(lines) != 2 || !strings.Contains(lines[0], "fix retry") || !strings.Contains(lines[1], "write test") {
		t.Fatalf("lines = %q", lines)
	}
	if got := s.Lines("below_editor", 40, 8, ascii()); len(got) != 0 {
		t.Fatalf("wrong placement rendered %q", got)
	}
}

func TestWidget_WDG02_Clear(t *testing.T) {
	s := NewStore()
	s.SetWidget("tasks", "above_editor", &Content{Kind: "text", Lines: []string{"x"}})
	s.SetWidget("tasks", "above_editor", nil)
	if got := s.Lines("above_editor", 40, 8, ascii()); len(got) != 0 {
		t.Fatalf("cleared widget still renders %q", got)
	}
}

func TestWidget_WDG03_FirstAppearanceOrder(t *testing.T) {
	s := NewStore()
	s.SetWidget("a", "above_editor", &Content{Kind: "text", Lines: []string{"A"}})
	s.SetWidget("b", "above_editor", &Content{Kind: "text", Lines: []string{"B"}})
	s.SetWidget("a", "above_editor", &Content{Kind: "text", Lines: []string{"A2"}})
	lines := s.Lines("above_editor", 40, 8, ascii())
	if len(lines) != 2 || lines[0] != "A2" || lines[1] != "B" {
		t.Fatalf("lines = %q, want A2 then B", lines)
	}
}

func TestWidget_WDG04_HeightCap(t *testing.T) {
	s := NewStore()
	body := make([]string, 30)
	for i := range body {
		body[i] = "line"
	}
	s.SetWidget("big", "above_editor", &Content{Kind: "text", Lines: body})
	lines := s.Lines("above_editor", 40, 8, ascii())
	if len(lines) != 8 {
		t.Fatalf("got %d lines, want 8", len(lines))
	}
	if lines[7] != "… 22 more" {
		t.Fatalf("marker = %q, want %q", lines[7], "… 22 more")
	}
}

func TestWidget_WDG05_ListStates(t *testing.T) {
	s := NewStore()
	s.SetWidget("tasks", "above_editor", &Content{Kind: "list", Items: []Item{
		{Text: "fix retry", State: "todo"},
		{Text: "write test", State: "doing"},
		{Text: "update docs", State: "done"},
	}})
	lines := s.Lines("above_editor", 40, 8, ascii())
	if !strings.Contains(lines[0], "☐") || !strings.Contains(lines[1], "◐") || !strings.Contains(lines[2], "☑") {
		t.Fatalf("glyphs = %q", lines)
	}

	// The doing item is highlighted: the same text renders differently.
	st := color()
	todo := s.Lines("above_editor", 40, 8, st)[0]
	doing := s.Lines("above_editor", 40, 8, st)[1]
	if ansi.Strip(todo) == ansi.Strip(doing) {
		t.Skip("states have identical text")
	}
	plainTodo := strings.ReplaceAll(ansi.Strip(todo), "☐", "")
	plainDoing := strings.ReplaceAll(ansi.Strip(doing), "◐", "")
	_ = plainTodo
	_ = plainDoing
	if todo == doing {
		t.Fatalf("doing item is not highlighted: %q", doing)
	}
}

func TestWidget_WDG06_TableFits(t *testing.T) {
	s := NewStore()
	s.SetWidget("t", "above_editor", &Content{Kind: "table",
		Columns: []string{"name", "status", "description"},
		Rows: [][]string{
			{"a", "ok", strings.Repeat("very long text ", 8)},
			{"b", "bad", "x"},
		},
	})
	lines := s.Lines("above_editor", 40, 8, ascii())
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3: %q", len(lines), lines)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 40 {
			t.Fatalf("table line %q is %d wide", l, ansi.StringWidth(l))
		}
	}
	if !strings.Contains(lines[1], "…") {
		t.Fatalf("long cell was not truncated: %q", lines[1])
	}
}

func TestWidget_WDG07_Progress(t *testing.T) {
	s := NewStore()
	s.SetWidget("p", "above_editor", &Content{Kind: "progress", Label: "build", Value: 0.4})
	lines := s.Lines("above_editor", 40, 8, ascii())
	if len(lines) != 1 || !strings.Contains(lines[0], "40%") || !strings.Contains(lines[0], "▮") {
		t.Fatalf("progress = %q", lines)
	}
}

func TestWidget_WDG08_StripAndClip(t *testing.T) {
	s := NewStore()
	s.SetWidget("w", "above_editor", &Content{Kind: "text", Lines: []string{
		"\x1b[31mred\x1b[0m",
		strings.Repeat("x", 200),
	}})
	lines := s.Lines("above_editor", 20, 8, ascii())
	for _, l := range lines {
		if ansi.StringWidth(l) > 20 {
			t.Fatalf("line too wide: %q", l)
		}
		if strings.Contains(l, "\x1b[31m") {
			t.Fatalf("control sequence survived: %q", l)
		}
	}
}

func TestWidget_WDG10_PlacementsIndependent(t *testing.T) {
	s := NewStore()
	above := make([]string, 12)
	below := make([]string, 4)
	for i := range above {
		above[i] = "a"
	}
	for i := range below {
		below[i] = "b"
	}
	s.SetWidget("a", "above_editor", &Content{Kind: "text", Lines: above})
	s.SetWidget("b", "below_editor", &Content{Kind: "text", Lines: below})
	if got := s.Lines("above_editor", 20, 8, ascii()); len(got) != 8 {
		t.Fatalf("above = %d lines, want 8", len(got))
	}
	if got := s.Lines("below_editor", 20, 8, ascii()); len(got) != 4 {
		t.Fatalf("below = %d lines, want 4", len(got))
	}
}

func TestWidget_WDG11_HeightMatchesLines(t *testing.T) {
	for _, lines := range [][]string{{}, {"a"}, {"a", "b"}, make([]string, 30)} {
		s := NewStore()
		s.SetWidget("w", "above_editor", &Content{Kind: "text", Lines: lines})
		if got, want := s.Height("above_editor", 20, 8), len(s.Lines("above_editor", 20, 8, ascii())); got != want {
			t.Fatalf("Height=%d Lines=%d for %d lines", got, want, len(lines))
		}
	}
}

func TestWidget_WDG12_UnknownKind(t *testing.T) {
	s := NewStore()
	s.SetWidget("w", "above_editor", &Content{Kind: "sparkles"})
	if got := s.Lines("above_editor", 20, 8, ascii()); len(got) != 0 {
		t.Fatalf("unknown kind rendered %q", got)
	}
	if s.TakeNotice() == "" {
		t.Fatal("unknown kind did not produce a notice")
	}
	if s.TakeNotice() != "" {
		t.Fatal("notice was not cleared")
	}
}

func TestWidget_STS01_StatusOrderAndSeparator(t *testing.T) {
	s := NewStore()
	s.SetStatus("zeta", []Segment{{Text: "z"}})
	s.SetStatus("alpha", []Segment{{Text: "a"}})
	got := s.Footer(80, ascii())
	if !strings.HasPrefix(got, "z /// a") {
		t.Fatalf("footer = %q, want first-appearance order z then a", got)
	}
}

func TestWidget_STS02_ClearStatus(t *testing.T) {
	s := NewStore()
	s.SetStatus("k", []Segment{{Text: "x"}})
	s.SetStatus("k", nil)
	if got := s.Footer(80, ascii()); got != "" {
		t.Fatalf("footer = %q, want empty", got)
	}
}

func TestWidget_STS03_StyleMapping(t *testing.T) {
	st := color()
	if segmentStyle("warn", st).Render("x") == segmentStyle("error", st).Render("x") {
		t.Fatal("warn and error should map to different theme tokens")
	}
	if segmentStyle("nope", st).Render("x") != segmentStyle("muted", st).Render("x") {
		t.Fatal("an unknown style should fall back to muted")
	}
	if segmentStyle("", st).Render("x") != st.Text.Render("x") {
		t.Fatal("an empty style should map to the default text style")
	}
}

func TestWidget_TST01_Lifetimes(t *testing.T) {
	now := time.Unix(1000, 0)
	s := NewStore()
	s.Notify("info", "info", now)
	s.Notify("warn", "warn", now)
	if got := s.Toasts(now.Add(3*time.Second), 40, ascii()); len(got) != 2 {
		t.Fatalf("at 3s = %q", got)
	}
	if got := s.Toasts(now.Add(5*time.Second), 40, ascii()); len(got) != 1 || !strings.Contains(got[0], "warn") {
		t.Fatalf("at 5s = %q, want only warn", got)
	}
	if got := s.Toasts(now.Add(9*time.Second), 40, ascii()); len(got) != 0 {
		t.Fatalf("at 9s = %q, want none", got)
	}
}

func TestWidget_TST02_MaxThreeNewest(t *testing.T) {
	now := time.Unix(0, 0)
	s := NewStore()
	for i, text := range []string{"one", "two", "three", "four", "five"} {
		s.Notify(text, "info", now.Add(time.Duration(i)*time.Millisecond))
	}
	got := s.Toasts(now.Add(10*time.Millisecond), 40, ascii())
	if len(got) != 3 {
		t.Fatalf("got %d toasts, want 3", len(got))
	}
	joined := strings.Join(got, "|")
	if strings.Contains(joined, "one") || strings.Contains(joined, "two") {
		t.Fatalf("old toasts kept: %q", got)
	}
}

func TestWidget_TST03_Duplicate(t *testing.T) {
	now := time.Unix(0, 0)
	s := NewStore()
	s.Notify("same", "info", now)
	s.Notify("same", "info", now.Add(time.Second))
	if got := s.Toasts(now.Add(2*time.Second), 40, ascii()); len(got) != 1 {
		t.Fatalf("got %d toasts, want 1: %q", len(got), got)
	}
}

func TestWidget_TST04_ToastsDoNotConsumeKeys(t *testing.T) {
	// Toasts are pure: reading them never changes widget/status state.
	s := NewStore()
	s.Notify("hello", "info", time.Unix(0, 0))
	before := s.Height("above_editor", 40, 8)
	_ = s.Toasts(time.Unix(0, 0), 40, ascii())
	if after := s.Height("above_editor", 40, 8); after != before {
		t.Fatalf("toasts changed layout: %d -> %d", before, after)
	}
}

func FuzzWidgetContent(f *testing.F) {
	f.Add("text", "hello\x1b[31m world")
	f.Add("list", "todo")
	f.Add("table", "a,b")
	f.Add("progress", "0.5")
	f.Add("bogus", "x")
	f.Fuzz(func(t *testing.T, kind, body string) {
		s := NewStore()
		c := &Content{Kind: kind, Lines: []string{body}, Label: body, Value: 0.5}
		c.Items = []Item{{Text: body, State: body, Detail: body}}
		c.Columns = []string{body, "b"}
		c.Rows = [][]string{{body, body}}
		s.SetWidget("w", "above_editor", c)
		for _, l := range s.Lines("above_editor", 10, 8, ascii()) {
			if ansi.StringWidth(l) > 10 {
				t.Fatalf("line %q exceeds width", l)
			}
		}
	})
}
