//go:build !notui

package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/clipboard"
	"github.com/AutumnsGrove/nova/media"
	"github.com/AutumnsGrove/nova/proto"
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
			m.notice = "no image on the clipboard"
		case errors.Is(msg.err, clipboard.ErrUnavailable):
			m.notice = "no clipboard available on this host"
		default:
			m.notice = msg.err.Error()
		}
		return
	}
	name := msg.img.Name
	if name == "" {
		name = "clipboard.png"
	}
	m.pendingAtts = append(m.pendingAtts, pendingAttachment{Name: name, MimeType: msg.img.MimeType, Data: msg.img.Data})
}

// attachmentRefs converts pending attachments to protocol attachments.
func (m *Model) attachmentRefs() []proto.Attachment {
	if len(m.pendingAtts) == 0 {
		return nil
	}
	out := make([]proto.Attachment, len(m.pendingAtts))
	for i, a := range m.pendingAtts {
		out[i] = proto.Attachment{Kind: "image", Name: a.Name, MimeType: a.MimeType, Data: base64.StdEncoding.EncodeToString(a.Data)}
	}
	return out
}

// attachmentNames returns the pending attachments' display names.
func (m *Model) attachmentNames() []string {
	out := make([]string, len(m.pendingAtts))
	for i, a := range m.pendingAtts {
		out[i] = a.Name
	}
	return out
}

// attachPaths converts whitespace-delimited tokens in text that name an image
// file into pending attachments, replacing each token with the file's base
// name. A token that is not an image is left alone.
func (m *Model) attachPaths(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return text
	}
	changed := false
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
			abs = filepath.Join(m.cwd, abs)
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() > maxPathAttachBytes {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		mime, ok := media.Sniff(data)
		if !ok {
			continue
		}
		name := filepath.Base(abs)
		m.pendingAtts = append(m.pendingAtts, pendingAttachment{Name: name, MimeType: mime, Data: data})
		fields[i] = name
		changed = true
	}
	if !changed {
		return text
	}
	return strings.Join(fields, " ")
}

// chipLine renders the pending attachments as one muted line, or "".
func (m *Model) chipLine() string {
	if len(m.pendingAtts) == 0 {
		return ""
	}
	names := make([]string, len(m.pendingAtts))
	for i, a := range m.pendingAtts {
		names[i] = a.Name + " ×"
	}
	return m.st.Muted.Render("attached: " + strings.Join(names, "  "))
}
