package mode

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/AutumnsGrove/nova/media"
	"github.com/AutumnsGrove/nova/mode/attach"
	"github.com/AutumnsGrove/nova/proto"
)

// StoreAttachment keeps an uploaded image for later turns and returns its id.
// It checks and bounds the bytes here; attach.Message normalizes them again on
// the way into a prompt. The store lives in memory and expires with the server.
func (s *Server) StoreAttachment(name, mime string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty attachment")
	}
	if len(data) > attach.MaxBytes {
		return "", fmt.Errorf("attachment is larger than %d MiB", attach.MaxBytes>>20)
	}
	sniffed, ok := media.Sniff(data)
	if !ok {
		return "", fmt.Errorf("attachment is not a supported image")
	}
	id := newAttachmentID()
	s.mu.Lock()
	if s.attachments == nil {
		s.attachments = map[string]proto.Attachment{}
	}
	s.attachments[id] = proto.Attachment{Kind: "image", Name: name, MimeType: sniffed, Data: base64.StdEncoding.EncodeToString(data)}
	s.mu.Unlock()
	return id, nil
}

// newAttachmentID returns a random opaque id for one stored upload.
func newAttachmentID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// resolveAttachments replaces an attachment that names a stored id with the
// stored bytes, returning a copy when anything changes. An unknown id is an
// error, so a prompt never silently loses an upload.
func (s *Server) resolveAttachments(atts []proto.Attachment) ([]proto.Attachment, error) {
	out := atts
	changed := false
	for i, a := range atts {
		if a.ID == "" {
			continue
		}
		s.mu.Lock()
		stored, ok := s.attachments[a.ID]
		s.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("attachment %q is unknown or expired", a.ID)
		}
		if a.Name != "" {
			stored.Name = a.Name
		}
		if !changed {
			out = append([]proto.Attachment(nil), atts...)
			changed = true
		}
		out[i] = stored
	}
	return out, nil
}
