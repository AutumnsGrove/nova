// Package attach turns protocol attachments into transcript content. It sits
// under mode so the server core stays small: the limits on how many images a
// prompt may carry and how large they may be live here, beside the conversion,
// and every front door reaches them through the same function.
package attach

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"github.com/AutumnsGrove/nova/media"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
)

const (
	// MaxImages is how many images one prompt may carry.
	MaxImages = 4
	// MaxBytes is the most decoded image data one prompt may carry, and the
	// most one stored upload may hold. Together with MaxImages it keeps a client
	// from sending a request no session line or provider allows.
	MaxBytes = 8 << 20
)

// Message builds a user message from text and attachments, normalizing every
// image into a bounded part. acceptsImages is the selected model's capability;
// a text-only model is refused here, before a run starts, rather than left to
// the loop's omission note.
func Message(text string, atts []proto.Attachment, acceptsImages bool) (message.Message, error) {
	msg := message.User(text)
	if len(atts) == 0 {
		return msg, nil
	}
	var parts []message.ContentPart
	images, total := 0, 0
	for _, a := range atts {
		kind := a.Kind
		if kind == "" && strings.HasPrefix(a.MimeType, "image/") {
			kind = "image"
		}
		switch kind {
		case "text":
			if a.Text != "" {
				if msg.Content != "" {
					msg.Content += "\n\n"
				}
				msg.Content += a.Text
			}
		case "image":
			data, err := attachmentBytes(a)
			if err != nil {
				return message.Message{}, err
			}
			if images >= MaxImages {
				return message.Message{}, fmt.Errorf("at most %d images per prompt", MaxImages)
			}
			if total+len(data) > MaxBytes {
				return message.Message{}, fmt.Errorf("images per prompt must total at most %d MiB", MaxBytes>>20)
			}
			images++
			total += len(data)
			res, err := media.Normalize(data, a.MimeType, a.Name)
			if err != nil {
				return message.Message{}, fmt.Errorf("attachment %q: %w", a.Name, err)
			}
			parts = append(parts, res.Part)
		default:
			return message.Message{}, fmt.Errorf("unsupported attachment kind %q", kind)
		}
	}
	if len(parts) > 0 {
		if !acceptsImages {
			return message.Message{}, fmt.Errorf("the selected model does not support images")
		}
		msg = msg.WithParts(parts...)
	}
	return msg, nil
}

// attachmentBytes returns an image attachment's bytes. Only inline data is
// accepted: a remote client must never be able to name a server path.
func attachmentBytes(a proto.Attachment) ([]byte, error) {
	if a.Path != "" && a.Data == "" {
		return nil, fmt.Errorf("attachment %q: send data, not a path", a.Name)
	}
	if a.Data == "" {
		return nil, fmt.Errorf("attachment %q has no data", a.Name)
	}
	if base64.StdEncoding.DecodedLen(len(a.Data)) > MaxBytes {
		return nil, fmt.Errorf("attachment %q is too large", a.Name)
	}
	data, err := base64.StdEncoding.DecodeString(a.Data)
	if err != nil {
		return nil, fmt.Errorf("attachment %q is not base64: %w", a.Name, err)
	}
	return data, nil
}

// Accepts reports whether a model's input modalities include images.
func Accepts(mods []string) bool { return slices.Contains(mods, "image") }
