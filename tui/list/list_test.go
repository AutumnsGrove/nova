//go:build !notui

package list

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// testItem is a scriptable Item. renders counts Render calls when non-nil.
type testItem struct {
	id      string
	version int
	lines   []string
	render  func(width int) string
	renders *int
}

func (t *testItem) ID() string   { return t.id }
func (t *testItem) Version() int { return t.version }
func (t *testItem) Render(width int) string {
	if t.renders != nil {
		*t.renders++
	}
	if t.render != nil {
		return t.render(width)
	}
	return strings.Join(t.lines, "\n")
}

func oneLine(id, text string) *testItem {
	return &testItem{id: id, lines: []string{text}}
}

func viewLines(t *testing.T, l *List) []string {
	t.Helper()
	v := l.View()
	if v == "" {
		return nil
	}
	raw := strings.Split(v, "\n")
	lines := make([]string, len(raw))
	for i, line := range raw {
		lines[i] = strings.TrimRight(line, " ")
	}
	return lines
}

func TestList_VLS01_FollowsNewContent(t *testing.T) {
	l := New()
	l.Resize(20, 3)
	for i := 0; i < 10; i++ {
		l.Append(oneLine(string(rune('a'+i)), "line"+string(rune('0'+i))))
	}
	got := viewLines(t, l)
	want := []string{"line7", "line8", "line9"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("following view = %v, want %v", got, want)
	}
}

func TestList_VLS02_ScrollingUpStopsFollowing(t *testing.T) {
	l := New()
	l.Resize(20, 3)
	for i := 0; i < 10; i++ {
		l.Append(oneLine(string(rune('a'+i)), "line"+string(rune('0'+i))))
	}
	l.ScrollBy(-3)
	frozen := strings.Join(viewLines(t, l), "|")
	if l.Following() {
		t.Fatal("Following is still true after scrolling up")
	}
	l.Append(oneLine("z", "lineZ"))
	if got := strings.Join(viewLines(t, l), "|"); got != frozen {
		t.Fatalf("view moved after append while not following: %q != %q", got, frozen)
	}
}

func TestList_VLS03_ScrollToBottomResumes(t *testing.T) {
	l := New()
	l.Resize(20, 3)
	for i := 0; i < 10; i++ {
		l.Append(oneLine(string(rune('a'+i)), "line"+string(rune('0'+i))))
	}
	l.ScrollBy(-5)
	l.ScrollToBottom()
	if !l.Following() {
		t.Fatal("Following is false after ScrollToBottom")
	}
	l.Append(oneLine("z", "lineZ"))
	got := viewLines(t, l)
	if last := got[len(got)-1]; last != "lineZ" {
		t.Fatalf("new content not shown after ScrollToBottom: %v", got)
	}
}

func TestList_VLS04_ViewHasExactLines(t *testing.T) {
	for _, width := range []int{1, 5, 20, 80} {
		for _, height := range []int{0, 1, 3, 10} {
			l := New()
			l.Resize(width, height)
			for i := 0; i < 40; i++ {
				l.Append(oneLine("i", "a long line of text that will not fit"))
			}
			v := l.View()
			if height == 0 {
				if v != "" {
					t.Fatalf("%dx%d: view = %q, want empty", width, height, v)
				}
				continue
			}
			lines := strings.Split(v, "\n")
			if len(lines) != height {
				t.Fatalf("%dx%d: view has %d lines, want %d", width, height, len(lines), height)
			}
			for _, line := range lines {
				if w := ansi.StringWidth(line); w > width {
					t.Fatalf("%dx%d: line %q is %d wide", width, height, line, w)
				}
			}
		}
	}
}

func TestList_VLS05_OnlyVisibleItemsRendered(t *testing.T) {
	l := New()
	l.Resize(40, 30)
	for i := 0; i < 10000; i++ {
		l.Append(oneLine("i", "row"))
	}
	l.View()
	if l.RenderCount() >= 100 {
		t.Fatalf("first view rendered %d items, want under 100", l.RenderCount())
	}
}

func TestList_VLS06_RenderCache(t *testing.T) {
	l := New()
	l.Resize(40, 5)
	for i := 0; i < 20; i++ {
		l.Append(oneLine("i", "row"))
	}
	l.View()
	before := l.RenderCount()
	l.View()
	if l.RenderCount() != before {
		t.Fatalf("second view added %d renders, want 0", l.RenderCount()-before)
	}
}

func TestList_VLS07_TouchInvalidatesOne(t *testing.T) {
	l := New()
	l.Resize(40, 5)
	for i := 0; i < 20; i++ {
		l.Append(oneLine(string(rune('a'+i)), "row"))
	}
	l.View()
	before := l.RenderCount()
	l.Touch("t")
	l.View()
	if got := l.RenderCount() - before; got != 1 {
		t.Fatalf("Touch caused %d renders, want 1", got)
	}
}

func TestList_VLS08_VersionChangeInvalidates(t *testing.T) {
	l := New()
	l.Resize(40, 5)
	it := oneLine("a", "row")
	l.Append(it)
	l.View()
	before := l.RenderCount()
	it.version++
	l.View()
	if got := l.RenderCount() - before; got != 1 {
		t.Fatalf("version change caused %d renders, want 1", got)
	}
}

func TestList_VLS09_ResizeRendersLazily(t *testing.T) {
	l := New()
	l.Resize(40, 5)
	for i := 0; i < 1000; i++ {
		l.Append(oneLine("i", "row"))
	}
	l.View()
	before := l.RenderCount()
	l.Resize(30, 5)
	l.View()
	if got := l.RenderCount() - before; got > 10 {
		t.Fatalf("resize re-rendered %d items, want only the visible few", got)
	}
}

func TestList_VLS10_ScrollClamps(t *testing.T) {
	l := New()
	l.Resize(20, 4)
	for i := 0; i < 10; i++ {
		l.Append(oneLine("i", "row"))
	}
	l.ScrollBy(1_000_000)
	l.View()
	l.ScrollBy(-1_000_000)
	l.View()
	if l.Following() {
		t.Fatal("Following should be false after scrolling to the top")
	}
}

func TestList_VLS11_EmptyList(t *testing.T) {
	l := New()
	l.Resize(20, 3)
	lines := viewLines(t, l)
	if len(lines) != 3 {
		t.Fatalf("empty list view has %d lines, want 3", len(lines))
	}
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			t.Fatalf("empty list line is not blank: %q", line)
		}
	}
}

func TestList_VLS12_ItemTallerThanViewport(t *testing.T) {
	l := New()
	l.Resize(20, 5)
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "row")
	}
	l.Append(&testItem{id: "big", lines: lines})
	// Following shows the tail of the tall item.
	if got := viewLines(t, l); len(got) != 5 {
		t.Fatalf("view has %d lines, want 5", len(got))
	}
	l.ScrollBy(-10)
	if got := viewLines(t, l); len(got) != 5 {
		t.Fatalf("scrolled view has %d lines, want 5", len(got))
	}
	l.ScrollBy(-1_000_000)
	if !strings.HasPrefix(strings.Join(viewLines(t, l), "\n"), "row") {
		t.Fatal("could not scroll to the top of a tall item")
	}
}

func TestList_VLS13_ZeroLineItems(t *testing.T) {
	l := New()
	l.Resize(20, 2)
	l.Append(&testItem{id: "empty", lines: nil})
	l.Append(oneLine("a", "row"))
	l.Append(&testItem{id: "empty2"})
	l.View()
	if got := viewLines(t, l); len(got) != 2 {
		t.Fatalf("view has %d lines, want 2", len(got))
	}
}

func TestList_VLS15_SyncPreservesPosition(t *testing.T) {
	l := New()
	l.Resize(20, 3)
	items := make([]Item, 10)
	for i := range items {
		items[i] = oneLine("item"+string(rune('a'+i)), "line"+string(rune('0'+i)))
	}
	l.SetItems(items)
	l.ScrollBy(-3)
	if l.Following() {
		t.Fatal("scrolling up left Following true")
	}
	frozen := strings.Join(viewLines(t, l), "|")
	l.Sync(append(items, oneLine("new", "lineZ")))
	if l.Following() {
		t.Fatal("Sync turned following back on")
	}
	if got := strings.Join(viewLines(t, l), "|"); got != frozen {
		t.Fatalf("view moved after Sync: %q != %q", got, frozen)
	}
}

func TestList_VLS16_SyncKeepsFollowing(t *testing.T) {
	l := New()
	l.Resize(20, 3)
	items := make([]Item, 5)
	for i := range items {
		items[i] = oneLine("item"+string(rune('a'+i)), "line"+string(rune('0'+i)))
	}
	l.SetItems(items)
	l.Sync(append(items, oneLine("new", "lineZ")))
	if !l.Following() {
		t.Fatal("Sync stopped following")
	}
	got := viewLines(t, l)
	if last := got[len(got)-1]; last != "lineZ" {
		t.Fatalf("new content not shown after Sync: %v", got)
	}
}

func TestList_VLS17_SyncClampsWhenShrunk(t *testing.T) {
	l := New()
	l.Resize(20, 3)
	items := make([]Item, 10)
	for i := range items {
		items[i] = oneLine("item"+string(rune('a'+i)), "line"+string(rune('0'+i)))
	}
	l.SetItems(items)
	l.ScrollBy(-4)
	l.Sync(items[:2])
	if got := viewLines(t, l); len(got) != 3 {
		t.Fatalf("view has %d lines, want 3: %v", len(got), got)
	}
}

func TestList_VLS18_SyncReusesCache(t *testing.T) {
	l := New()
	l.Resize(40, 5)
	items := make([]Item, 20)
	for i := range items {
		items[i] = oneLine("item"+string(rune('a'+i)), "row")
	}
	l.SetItems(items)
	l.View()
	before := l.RenderCount()
	l.Sync(items)
	l.View()
	if got := l.RenderCount() - before; got != 0 {
		t.Fatalf("Sync re-rendered %d items, want 0", got)
	}
}

func TestList_VLS19_ItemAt(t *testing.T) {
	l := New()
	l.Resize(20, 5)
	a := &testItem{id: "a", lines: []string{"a1", "a2"}}
	b := &testItem{id: "b", lines: []string{"b1", "b2", "b3"}}
	c := &testItem{id: "c", lines: []string{"c1"}}
	l.SetItems([]Item{a, b, c})
	var got []string
	for y := 0; y < 5; y++ {
		if it := l.ItemAt(y); it != nil {
			got = append(got, it.ID())
		}
	}
	if want := "a,b,b,b,c"; strings.Join(got, ",") != want {
		t.Fatalf("ItemAt = %v, want %s", got, want)
	}
	if l.ItemAt(5) != nil {
		t.Fatal("ItemAt beyond the viewport should be nil")
	}
}

// BenchmarkVLS14_Append measures the cost of growing a large list.
func BenchmarkList_VLS14_Append(b *testing.B) {
	l := New()
	l.Resize(40, 30)
	items := make([]*testItem, 10000)
	for i := range items {
		items[i] = oneLine("i", "row")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Append(items[i%len(items)])
	}
}
