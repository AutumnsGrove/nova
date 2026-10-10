package sdk

import (
	"context"
	"encoding/json"
)

// UI sends declarative UI requests to the host.
type UI struct{ host *Host }

// Choice is one option in a select.
type Choice struct {
	ID          string
	Label       string
	Description string
}

// Segment is one status segment.
type Segment struct {
	Text  string
	Style string
}

// Notify shows a fire-and-forget message.
func (u *UI) Notify(text string, level ...string) error {
	lvl := "info"
	if len(level) > 0 {
		lvl = level[0]
	}
	return u.host.send(map[string]any{"type": "ui_request", "method": "notify", "text": text, "level": lvl})
}

// Status sets a status segment.
func (u *UI) Status(key string, segments []Segment) error {
	return u.host.send(map[string]any{"type": "ui_request", "method": "status", "key": key, "segments": segments})
}

// Widget sets a widget.
func (u *UI) Widget(key, placement string, content any) error {
	return u.host.send(map[string]any{"type": "ui_request", "method": "widget", "key": key, "placement": placement, "content": content})
}

// Confirm asks a yes/no question.
func (u *UI) Confirm(ctx context.Context, title, message string) (bool, error) {
	raw, err := u.host.request(ctx, map[string]any{"type": "ui_request", "method": "confirm", "title": title, "message": message})
	if err != nil {
		return false, err
	}
	var r struct {
		OK bool `json:"ok"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.OK, nil
}

// Select asks the user to choose options.
func (u *UI) Select(ctx context.Context, title string, options []Choice, multi bool) ([]string, error) {
	raw, err := u.host.request(ctx, map[string]any{"type": "ui_request", "method": "select", "title": title, "options": options, "multi": multi})
	if err != nil {
		return nil, err
	}
	var r struct {
		IDs []string `json:"ids"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.IDs, nil
}

// Input asks for free text.
func (u *UI) Input(ctx context.Context, title, placeholder string) (string, error) {
	raw, err := u.host.request(ctx, map[string]any{"type": "ui_request", "method": "input", "title": title, "placeholder": placeholder})
	if err != nil {
		return "", err
	}
	var r struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.Text, nil
}
