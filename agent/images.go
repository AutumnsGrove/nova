package agent

import (
	"slices"
	"strings"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// imageOmittedNote is what a request says in place of an image a text-only
// model cannot take, so the model knows something was there.
const imageOmittedNote = "[model does not support images; the image was omitted]"

// acceptsImages reports whether the current model declares image input.
func (r *run) acceptsImages() bool {
	mods := r.cfg.InputModalities
	if r.cfg.InputModalitiesFunc != nil {
		mods = r.cfg.InputModalitiesFunc()
	}
	return slices.Contains(mods, "image")
}

// gateImages drops image parts from the request when the model cannot take
// them, leaving an omission note in the text. The transcript keeps its parts,
// so the TUI still shows the image; only the wire loses it. It returns the
// original request when the model accepts images or nothing changes, so a
// text-only conversation does not copy its transcript on every turn.
func (r *run) gateImages(req provider.Request) provider.Request {
	if r.acceptsImages() {
		return req
	}
	if !slices.ContainsFunc(req.Messages, message.Message.HasImages) {
		return req
	}
	msgs := make([]message.Message, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = dropImages(m)
	}
	req.Messages = msgs
	return req
}

// dropImages returns a copy of m without image parts, with imageOmittedNote
// appended to its text when any were dropped.
func dropImages(m message.Message) message.Message {
	if !m.HasImages() {
		return m
	}
	m = m.Clone()
	kept := make([]message.ContentPart, 0, len(m.Parts))
	for _, p := range m.Parts {
		if p.Type == message.PartImage && (p.Data != "" || p.URL != "") {
			continue
		}
		kept = append(kept, p)
	}
	m.Parts = kept
	if m.Content == "" {
		m.Content = imageOmittedNote
	} else {
		m.Content = strings.TrimRight(m.Content, "\n") + "\n\n" + imageOmittedNote
	}
	return m
}

// toolImageMessage builds the one user message that carries every image a tool
// batch produced. Providers reject media on a tool message, so the images ride
// a synthetic user turn after the whole batch, in call order, with the results'
// captions joined as its text.
func toolImageMessage(outcomes []callOutcome) *message.Message {
	var parts []message.ContentPart
	var captions []string
	for _, o := range outcomes {
		if len(o.result.Parts) == 0 {
			continue
		}
		parts = append(parts, o.result.Parts...)
		if o.result.Caption != "" {
			captions = append(captions, o.result.Caption)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	text := strings.Join(captions, "\n")
	if text == "" {
		text = "[image from a tool result]"
	}
	m := message.UserParts(text, parts...)
	m.Synthetic = true
	return &m
}
