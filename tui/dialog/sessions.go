//go:build !notui

package dialog

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/sanitize"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// SessionInfo is one session row for the resume picker.
type SessionInfo struct {
	ID       string
	Path     string
	Name     string
	Modified time.Time
	Entries  int
	Current  bool
}

// SessionChoice is the session picker's answer. Fork requests a copy.
type SessionChoice struct {
	Path string
	Fork bool
}

// sessionList is the resume picker. Value is SessionChoice.
type sessionList struct {
	items  []SessionInfo
	cursor int
	scroll int
	now    time.Time
	done   bool
	value  SessionChoice
}

// NewSessionList builds the picker, newest first.
func NewSessionList(items []SessionInfo) Dialog {
	sorted := append([]SessionInfo(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Modified.After(sorted[j].Modified) })
	return &sessionList{items: sorted, now: time.Now()}
}

// ID is empty for a local dialog.
func (s *sessionList) ID() string { return "" }

// chosen returns the highlighted session path.
func (s *sessionList) chosen() string {
	if s.cursor < 0 || s.cursor >= len(s.items) {
		return ""
	}
	return s.items[s.cursor].Path
}

// HandleKey implements the picker keys.
func (s *sessionList) HandleKey(k tea.KeyPressMsg) Result {
	switch key := k.Keystroke(); key {
	case "esc", "ctrl+c":
		return Result{Done: true, Cancelled: true}
	case "ctrl+f":
		if path := s.chosen(); path != "" {
			s.done, s.value = true, SessionChoice{Path: path, Fork: true}
			return Result{Done: true, Value: s.value}
		}
	case "enter":
		if path := s.chosen(); path != "" {
			s.done, s.value = true, SessionChoice{Path: path}
			return Result{Done: true, Value: s.value}
		}
	case "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down":
		if s.cursor < len(s.items)-1 {
			s.cursor++
		}
	case "pgup":
		s.cursor = max(s.cursor-10, 0)
	case "pgdown":
		s.cursor = min(s.cursor+10, max(len(s.items)-1, 0))
	}
	return Result{}
}

// Tick does nothing for the picker.
func (s *sessionList) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (s *sessionList) Size(maxW, maxH int) (int, int) {
	return min(max(len("Sessions")+8, 56), maxW), min(len(s.items)+5, maxH)
}

// View renders the framed picker.
func (s *sessionList) View(w, h int, st styles.Styles) string {
	inner, rows := w-2, h-3
	var body []string
	if len(s.items) == 0 {
		body = append(body, "no sessions")
	} else {
		start, end := window(s.cursor, s.scroll, rows, len(s.items))
		s.scroll = start
		for i := start; i < end; i++ {
			body = append(body, s.line(i, inner, st))
		}
	}
	return frame("Sessions", body, "enter resume · ctrl+f fork · esc cancel", w, h, st)
}

// line renders one session row.
func (s *sessionList) line(i, inner int, st styles.Styles) string {
	it := s.items[i]
	marker := "  "
	if i == s.cursor {
		marker = "> "
	}
	name := it.Name
	if name == "" {
		name = "untitled"
	}
	line := marker + sanitize.Text(name)
	line += "  " + st.Muted.Render(fmt.Sprintf("%s · %d entries", age(it.Modified, s.now), it.Entries))
	if it.Current {
		line += " " + st.Success.Render("(current)")
	}
	return ansi.Truncate(line, inner, "…")
}

// age renders a coarse age for a session.
func age(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// TreeNode is one session entry in the tree view.
type TreeNode struct {
	ID       string
	Type     string
	Preview  string
	Children []TreeNode
}

// treeRow is a flattened node with its depth and active-path flag.
type treeRow struct {
	id      string
	typ     string
	preview string
	depth   int
	active  bool
}

// treeDialog is the branch tree. Value is the chosen entry id.
type treeDialog struct {
	rows   []treeRow
	cursor int
	scroll int
	done   bool
	value  string
}

// NewTree builds the tree, highlighting the active path and the current entry.
func NewTree(root []TreeNode, current string) Dialog {
	path := treePath(root, current)
	var rows []treeRow
	flattenTree(root, 0, path, &rows)
	cursor := 0
	for i, r := range rows {
		if r.id == current {
			cursor = i
			break
		}
	}
	return &treeDialog{rows: rows, cursor: cursor}
}

// treePath returns every id on the path from the root to current.
func treePath(root []TreeNode, current string) map[string]bool {
	path := map[string]bool{}
	var walk func(nodes []TreeNode, trail []string) bool
	walk = func(nodes []TreeNode, trail []string) bool {
		for _, n := range nodes {
			t := append(append([]string{}, trail...), n.ID)
			if n.ID == current {
				for _, id := range t {
					path[id] = true
				}
				return true
			}
			if walk(n.Children, t) {
				return true
			}
		}
		return false
	}
	walk(root, nil)
	return path
}

// flattenTree walks the tree into depth-first rows.
func flattenTree(nodes []TreeNode, depth int, path map[string]bool, out *[]treeRow) {
	for _, n := range nodes {
		*out = append(*out, treeRow{id: n.ID, typ: n.Type, preview: n.Preview, depth: depth, active: path[n.ID]})
		flattenTree(n.Children, depth+1, path, out)
	}
}

// ID is empty for a local dialog.
func (t *treeDialog) ID() string { return "" }

// HandleKey implements the tree keys.
func (t *treeDialog) HandleKey(k tea.KeyPressMsg) Result {
	switch key := k.Keystroke(); key {
	case "esc", "ctrl+c", "q":
		return Result{Done: true, Cancelled: true}
	case "enter":
		if t.cursor >= 0 && t.cursor < len(t.rows) {
			t.done, t.value = true, t.rows[t.cursor].id
			return Result{Done: true, Value: t.value}
		}
	case "up":
		if t.cursor > 0 {
			t.cursor--
		}
	case "down":
		if t.cursor < len(t.rows)-1 {
			t.cursor++
		}
	case "pgup":
		t.cursor = max(t.cursor-10, 0)
	case "pgdown":
		t.cursor = min(t.cursor+10, max(len(t.rows)-1, 0))
	}
	return Result{}
}

// Tick does nothing for the tree.
func (t *treeDialog) Tick(time.Time) bool { return false }

// Size returns the desired box size.
func (t *treeDialog) Size(maxW, maxH int) (int, int) {
	return min(max(len("Tree")+8, 56), maxW), min(len(t.rows)+3, maxH)
}

// View renders the framed tree.
func (t *treeDialog) View(w, h int, st styles.Styles) string {
	inner, rows := w-2, h-3
	var body []string
	start, end := window(t.cursor, t.scroll, rows, len(t.rows))
	t.scroll = start
	for i := start; i < end; i++ {
		body = append(body, treeLine(t.rows[i], i == t.cursor, inner, st))
	}
	return frame("Tree", body, "enter navigate · esc close", w, h, st)
}

// treeLine renders one tree row.
func treeLine(r treeRow, selected bool, inner int, st styles.Styles) string {
	indent := strings.Repeat("  ", r.depth)
	marker := "○ "
	style := st.Muted
	if r.active {
		marker, style = "● ", st.Spark
	}
	if selected {
		if len(indent) >= 2 {
			indent = "> " + indent[2:]
		} else {
			indent = "> "
		}
	}
	line := style.Render(indent+marker+sanitize.Text(r.id)) + " " + st.Dim.Render(sanitize.Text(r.preview))
	return ansi.Truncate(line, inner, "…")
}
