//go:build !notui

package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func digitKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func settingsRows() []Setting {
	return []Setting{
		{Key: "display.thinking_lines", Label: "Thinking lines", Value: 8, Min: 1, Max: 50},
		{Key: "display.tool_lines", Label: "Tool lines", Value: 6, Min: 1, Max: 50},
	}
}

// DGS01: enter saves the edited values and keeps the keys.
func TestSettings_DGS01_Save(t *testing.T) {
	d := NewSettings("Settings", settingsRows())
	d.HandleKey(rightKey) // thinking 8 -> 9
	d.HandleKey(downKey)
	d.HandleKey(leftKey) // tool 6 -> 5
	r := d.HandleKey(enterKey)
	if !r.Done || r.Cancelled {
		t.Fatalf("enter result = %+v", r)
	}
	got, ok := r.Value.([]Setting)
	if !ok || len(got) != 2 {
		t.Fatalf("value = %#v", r.Value)
	}
	if got[0].Key != "display.thinking_lines" || got[0].Value != 9 {
		t.Fatalf("thinking = %+v, want 9", got[0])
	}
	if got[1].Key != "display.tool_lines" || got[1].Value != 5 {
		t.Fatalf("tool = %+v, want 5", got[1])
	}
}

// DGS02: typing digits sets the value.
func TestSettings_DGS02_TypeDigits(t *testing.T) {
	d := NewSettings("Settings", settingsRows())
	d.HandleKey(digitKey('1'))
	d.HandleKey(digitKey('2'))
	r := d.HandleKey(enterKey)
	got := r.Value.([]Setting)
	if got[0].Value != 12 {
		t.Fatalf("typed value = %d, want 12", got[0].Value)
	}
}

// DGS03: values are clamped to the row's range.
func TestSettings_DGS03_Clamp(t *testing.T) {
	d := NewSettings("Settings", settingsRows())
	for _, r := range "999" {
		d.HandleKey(digitKey(r))
	}
	r := d.HandleKey(enterKey)
	if got := r.Value.([]Setting)[0].Value; got != 50 {
		t.Fatalf("over-max value = %d, want 50", got)
	}

	d2 := NewSettings("Settings", settingsRows())
	for i := 0; i < 20; i++ {
		d2.HandleKey(leftKey)
	}
	r2 := d2.HandleKey(enterKey)
	if got := r2.Value.([]Setting)[0].Value; got != 1 {
		t.Fatalf("under-min value = %d, want 1", got)
	}
}

// DGS04: esc cancels without a value.
func TestSettings_DGS04_Cancel(t *testing.T) {
	d := NewSettings("Settings", settingsRows())
	r := d.HandleKey(escKey)
	if !r.Done || !r.Cancelled || r.Value != nil {
		t.Fatalf("esc result = %+v", r)
	}
}
