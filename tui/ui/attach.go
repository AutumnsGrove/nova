//go:build !notui

package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AutumnsGrove/nova/clipboard"
	"github.com/AutumnsGrove/nova/proto"
)

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
