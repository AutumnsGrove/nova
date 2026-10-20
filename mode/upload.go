package mode

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"slices"

	"github.com/AutumnsGrove/nova/media"
	"github.com/AutumnsGrove/nova/mode/attach"
	"github.com/AutumnsGrove/nova/proto"
)

// StoreAttachment keeps an uploaded image for later turns and returns its id
// and the mime type the bytes really are. It checks and bounds the bytes here;
// attach.Message normalizes them again on the way into a prompt. The store
// lives in memory and expires with the server. Callers pass no mime type of
// their own: a client's claim is only a hint a hostile client could get wrong.
func (s *Server) StoreAttachment(name string, data []byte) (id, mimeType string, err error) {
	if len(data) == 0 {
		return "", "", fmt.Errorf("empty attachment")
	}
	if len(data) > attach.MaxBytes {
		return "", "", fmt.Errorf("attachment is larger than %d MiB", attach.MaxBytes>>20)
	}
	sniffed, ok := media.Sniff(data)
	if !ok {
		return "", "", fmt.Errorf("attachment is not a supported image")
	}
	id = randomHex(16)
	s.mu.Lock()
	if s.attachments == nil {
		s.attachments = map[string]proto.Attachment{}
	}
	s.attachments[id] = proto.Attachment{Kind: "image", Name: name, MimeType: sniffed, Data: base64.StdEncoding.EncodeToString(data)}
	s.mu.Unlock()
	return id, sniffed, nil
}

// resolveAttachments returns a copy of atts in which an attachment that names a
// stored id is replaced by the stored bytes. An unknown id is an error, so a
// prompt never silently loses an upload.
func (s *Server) resolveAttachments(atts []proto.Attachment) ([]proto.Attachment, error) {
	out := slices.Clone(atts)
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
		stored.Name = cmp.Or(a.Name, stored.Name)
		out[i] = stored
	}
	return out, nil
}
