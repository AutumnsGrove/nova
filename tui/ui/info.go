//go:build !notui

package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/diag"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/dialog"
)

// overlayMaxLines caps the lines of untrusted text (a shell result, a crash
// report) an overlay keeps; it only bounds what the dialog holds.
const overlayMaxLines = 500

// crashLogTail is how much of the append-only crash log is read. The newest
// report is at the end, so a long-lived log never has to be loaded whole.
const crashLogTail = 256 << 10

// infoMsg carries the text of a read-only overlay that was built off the
// update path.
type infoMsg struct {
	title  string
	lines  []string
	intent string
}

// openHelp shows the key and command reference.
func (m *Model) openHelp() {
	keys := []dialog.KeyHelp{
		{Key: "enter", Description: "send prompt"},
		{Key: "alt+enter", Description: "queue a follow-up"},
		{Key: "esc", Description: "abort the run / close a dialog"},
		{Key: "ctrl+o", Description: "expand thinking and tools"},
		{Key: "click", Description: "expand or collapse one block"},
		{Key: "ctrl+g", Description: "edit in $EDITOR"},
		{Key: "ctrl+v", Description: "attach an image from the clipboard"},
		{Key: "ctrl+l", Description: "pick a model"},
		{Key: "ctrl+r", Description: "resume a session"},
		{Key: "ctrl+t", Description: "cycle thinking effort"},
		{Key: "ctrl+c twice", Description: "quit"},
	}
	all := m.allCommands()
	names := make([]string, 0, len(all)+1)
	names = append(names, "log")
	for _, c := range all {
		names = append(names, c.Name)
	}
	m.pushDialog(dialog.NewHelp(keys, names), intentHelp)
}

// showCrashLog opens the most recent crash report in a read-only overlay. It is
// a local command: the report lives on this machine, beside the TUI. The file
// is read by a command, not by Update.
func (m *Model) showCrashLog() tea.Cmd {
	rec := diag.Default()
	if rec == nil || rec.CrashPath() == "" {
		m.setNotice("no crash reports")
		return nil
	}
	path := rec.CrashPath()
	return func() tea.Msg {
		data, err := readTail(path, crashLogTail)
		if err != nil {
			return noticeMsg{text: "no crash reports"}
		}
		lines := outputLines(string(diag.LatestReport(data)), overlayMaxLines)
		if len(lines) == 0 {
			return noticeMsg{text: "no crash reports"}
		}
		return infoMsg{title: "Crash log", lines: lines, intent: intentCrash}
	}
}

// readTail returns the last n bytes of the file at path, or all of it when it
// is shorter.
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // read-only: a failed close loses nothing
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if size := info.Size(); size > n {
		if _, err := f.Seek(size-n, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek %s: %w", path, err)
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, n))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// showBashOutput opens the result of a `!cmd` in a read-only overlay. The
// server records it in the session, but nothing else tells the user what the
// command printed.
func (m *Model) showBashOutput(resp proto.Response) {
	if !resp.OK {
		if resp.Error != nil {
			m.setNotice(resp.Error.Message)
		}
		return
	}
	var out struct {
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal(resp.Data, &out) != nil {
		return
	}
	title := "Shell output"
	if out.IsError {
		title = "Shell command failed"
	}
	lines := outputLines(out.Content, overlayMaxLines)
	if len(lines) == 0 {
		lines = []string{"(no output)"}
	}
	m.pushDialog(dialog.NewInfo(title, lines), intentBash)
}

// outputLines turns untrusted text into at most limit plain lines plus a note
// when it was cut. Escape sequences, carriage returns, and other control
// characters are dropped so the text cannot move the cursor, retitle the
// terminal, or overwrite the overlay; tabs become spaces so widths hold, and
// bytes that are not valid UTF-8 (a tail can start mid-rune) are discarded.
func outputLines(s string, limit int) []string {
	s = strings.ReplaceAll(ansi.Strip(strings.ToValidUTF8(s, "")), "\t", "    ")
	s = strings.Map(func(r rune) rune {
		if r != '\n' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if extra := len(lines) - limit; extra > 0 {
		lines = append(lines[:limit], fmt.Sprintf("… %d more lines", extra))
	}
	return lines
}
