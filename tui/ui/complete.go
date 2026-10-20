//go:build !notui

package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
)

// completionMax is the number of entries shown in the popup.
const completionMax = 8

// completionItem is one selectable completion.
type completionItem struct {
	label string
	value string
}

// completion is the state of the command or path popup.
type completion struct {
	active bool
	kind   string // "command" | "path"
	items  []completionItem
	sel    int
	base   string // text before the token being completed
}

// completionMsg carries directory entries read by a command. text is the
// editor text the read was made for, so a late result can be told from a
// current one.
type completionMsg struct {
	kind  string
	text  string
	base  string
	items []completionItem
	err   error
}

// commandsLoadedMsg installs a command list, used by tests and future reloads.
type commandsLoadedMsg struct{ commands []commandInfo }

// openCommands filters the loaded slash commands by prefix, then substring.
func (m *Model) openCommands(prefix string) {
	items := filterCommands(m.allCommands(), prefix)
	if len(items) == 0 {
		m.completion.active = false
		return
	}
	if len(items) > completionMax {
		items = items[:completionMax]
	}
	m.completion = completion{active: true, kind: "command", items: items}
}

// filterCommands returns prefix matches first, then substring matches.
func filterCommands(commands []commandInfo, prefix string) []completionItem {
	var first, rest []completionItem
	for _, c := range commands {
		item := completionItem{label: c.Name, value: "/" + c.Name + " "}
		switch {
		case strings.HasPrefix(c.Name, prefix):
			first = append(first, item)
		case prefix != "" && strings.Contains(c.Name, prefix):
			rest = append(rest, item)
		}
	}
	return append(first, rest...)
}

// loadPaths reads the directory for an @ completion. It returns a command so
// the read happens off the update path.
func (m *Model) loadPaths(text string, at int) tea.Cmd {
	token := text[at+1:]
	dir, frag := filepath.Split(token)
	base := text[:at+1]
	cwd := m.cwd
	return func() tea.Msg {
		entries, err := os.ReadDir(filepath.Join(cwd, dir))
		if err != nil {
			return completionMsg{kind: "path", text: text, base: base, err: err}
		}
		var dirs, files []completionItem
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") && !strings.HasPrefix(frag, ".") {
				continue
			}
			if !strings.HasPrefix(name, frag) {
				continue
			}
			if e.IsDir() {
				dirs = append(dirs, completionItem{label: name + "/", value: dir + name + "/"})
			} else {
				files = append(files, completionItem{label: name, value: dir + name})
			}
		}
		items := slices.Concat(dirs, files)
		if len(items) > completionMax {
			items = items[:completionMax]
		}
		return completionMsg{kind: "path", text: text, base: base, items: items}
	}
}

// applyCompletion installs the results of a directory read. A read that
// finished after the text moved on is dropped: the edit that moved it on has
// already decided what the popup shows.
func (m *Model) applyCompletion(msg completionMsg) {
	if msg.text != m.editor.Value() {
		return
	}
	if msg.err != nil || len(msg.items) == 0 {
		m.completion.active = false
		return
	}
	m.completion = completion{active: true, kind: msg.kind, items: msg.items, base: msg.base}
}

// moveCompletion moves the selection by delta.
func (c *completion) move(delta int) {
	if !c.active || len(c.items) == 0 {
		return
	}
	c.sel = (c.sel + delta + len(c.items)) % len(c.items)
}

// acceptCompletion replaces the token with the selected item.
func (m *Model) acceptCompletion() {
	if !m.completion.active || len(m.completion.items) == 0 {
		return
	}
	item := m.completion.items[m.completion.sel]
	m.setEditorText(m.completion.base + item.value)
	m.completion.active = false
}

// lastAt returns the byte index of the @ that starts a path token, or -1. The
// @ must be at the start of the text or after whitespace.
func lastAt(text string) int {
	for i := len(text) - 1; i >= 0; i-- {
		if text[i] != '@' {
			continue
		}
		if i == 0 || text[i-1] == ' ' || text[i-1] == '\n' || text[i-1] == '\t' {
			return i
		}
		return -1
	}
	return -1
}

// completionView renders the popup at the given width.
func (m *Model) completionView(width int) []string {
	if !m.completion.active {
		return nil
	}
	var out []string
	for i, item := range m.completion.items {
		line := "  " + item.label
		if i == m.completion.sel {
			line = m.st.CompletionSelected.Render(line)
		} else {
			line = m.st.Completion.Render(line)
		}
		out = append(out, ansi.Truncate(line, width, ""))
	}
	return out
}
