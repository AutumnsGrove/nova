//go:build !notui

package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/clipboard"
	"github.com/AutumnsGrove/nova/media"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/sanitize"
)

// maxPathAttachBytes caps a file the editor reads for @path auto-attach, so a
// mistyped huge file cannot be pulled into memory.
const maxPathAttachBytes = 16 << 20

// pendingAttachment is an image attached to the next prompt but not yet sent.
type pendingAttachment struct {
	Name     string
	MimeType string
	Data     []byte
}

// clipboardMsg carries the result of a clipboard read performed by a tea.Cmd.
type clipboardMsg struct {
	img clipboard.Image
	err error
}

// pasteImage reads the clipboard image. Update does no I/O, so the read is a
// command; a host with no clipboard or no image becomes a notice.
func (m *Model) pasteImage() tea.Cmd {
	reader := m.clipboard
	if reader == nil {
		return func() tea.Msg { return clipboardMsg{err: clipboard.ErrUnavailable} }
	}
	return func() tea.Msg {
		img, err := reader.Read(context.Background())
		return clipboardMsg{img: img, err: err}
	}
}

// applyClipboard stores a read image as a pending attachment, or sets a notice.
func (m *Model) applyClipboard(msg clipboardMsg) {
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, clipboard.ErrNoImage):
			m.setNotice("no image on the clipboard")
		case errors.Is(msg.err, clipboard.ErrUnavailable):
			m.setNotice("no clipboard available on this host")
		default:
			m.setNotice(msg.err.Error())
		}
		return
	}
	name := msg.img.Name
	if name == "" {
		name = "clipboard.png"
	}
	m.pendingAtts = append(m.pendingAtts, pendingAttachment{Name: name, MimeType: msg.img.MimeType, Data: msg.img.Data})
}

// attachmentRefs converts attachments to protocol attachments.
func attachmentRefs(atts []pendingAttachment) []proto.Attachment {
	if len(atts) == 0 {
		return nil
	}
	out := make([]proto.Attachment, len(atts))
	for i, a := range atts {
		out[i] = proto.Attachment{Kind: "image", Name: a.Name, MimeType: a.MimeType, Data: base64.StdEncoding.EncodeToString(a.Data)}
	}
	return out
}

// attachmentNames returns the attachments' display names.
func attachmentNames(atts []pendingAttachment) []string {
	out := make([]string, len(atts))
	for i, a := range atts {
		out[i] = a.Name
	}
	return out
}

// attachPaths converts whitespace-delimited tokens in text that name an image
// file under cwd into attachments, replacing each token with the file's base
// name. A token that is not an image is left alone. It reads files, so it runs
// in a command and never in Update.
func attachPaths(cwd, text string) (string, []pendingAttachment) {
	fields := strings.Fields(text)
	var atts []pendingAttachment
	for i, f := range fields {
		path := strings.Trim(f, `"'`)
		// Path completion inserts an @ before the path; the @ is a mention
		// marker, not part of the file name.
		path = strings.TrimPrefix(path, "@")
		if path == "" || strings.HasPrefix(path, "-") {
			continue
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		data, ok := readImageFile(abs)
		if !ok {
			continue
		}
		mime, ok := media.Sniff(data)
		if !ok {
			continue
		}
		name := filepath.Base(abs)
		atts = append(atts, pendingAttachment{Name: name, MimeType: mime, Data: data})
		fields[i] = name
	}
	if len(atts) == 0 {
		return text, nil
	}
	return strings.Join(fields, " "), atts
}

// readImageFile reads a candidate attachment. Only a regular file of bounded
// size is opened: a prompt may mention /dev/zero or a named pipe, and reading
// either would never end, and the limit also holds if the file grows after the
// stat.
func readImageFile(path string) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPathAttachBytes {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }() // read-only: a failed close loses nothing
	data, err := io.ReadAll(io.LimitReader(f, maxPathAttachBytes))
	return data, err == nil
}

// chipLine renders the pending attachments as one muted line, or "".
func (m *Model) chipLine() string {
	if len(m.pendingAtts) == 0 {
		return ""
	}
	names := make([]string, len(m.pendingAtts))
	for i, a := range m.pendingAtts {
		names[i] = sanitize.Line(a.Name) + " ×"
	}
	return m.st.Muted.Render("attached: " + strings.Join(names, "  "))
}
