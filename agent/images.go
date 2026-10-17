package agent

import (
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
	for _, kind := range mods {
		if kind == "image" {
			return true
		}
	}
	return false
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
	changed := false
	for _, m := range req.Messages {
		if m.HasImages() {
			changed = true
			break
		}
	}
	if !changed {
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
