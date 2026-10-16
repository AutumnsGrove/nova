//go:build !notui

package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"gopkg.in/yaml.v3"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tui/chat"
	"github.com/AutumnsGrove/nova/tui/dialog"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// SETU01: /settings opens the dialog, and /config is an alias for it.
func TestUI_SETU01_OpensAndAlias(t *testing.T) {
	for _, name := range []string{"settings", "config"} {
		m, _, _ := newModel(t)
		m.setEditorText("/" + name)
		execAll(update(m, enterKey))
		if m.overlay.Len() != 1 {
			t.Fatalf("/%s did not open a dialog", name)
		}
		v := m.overlay.Top().View(60, 12, m.st)
		if !strings.Contains(v, "Thinking preview lines") || !strings.Contains(v, "Tool result lines") {
			t.Fatalf("/%s dialog does not list the settings: %q", name, v)
		}
	}
}

// SETU02: applying the dialog changes the display live and re-renders the
// existing cards; the new tool height caps the preview.
func TestUI_SETU02_ApplyLive(t *testing.T) {
	m, _, _ := newModel(t)
	m.home = t.TempDir()
	m.transcript.Apply(proto.Event{Type: proto.EventToolCall, Call: &proto.ToolCall{
		ID: "c1", Name: "bash", Arguments: []byte(`{"command":"ls"}`),
	}})
	m.transcript.Apply(proto.Event{Type: proto.EventToolEnd, CallID: "c1", Result: &proto.ToolResult{
		Content: "a\nb\nc\nd\ne\nf\ng", Details: []byte(`{"exit_code":0}`),
	}})
	m.syncList()
	before := m.transcript.Items()[0].Render(60)

	cmd := m.applySettings([]dialog.Setting{{Key: "display.tool_lines", Label: "Tool result lines", Value: 2, Min: 1, Max: 50}})
	if m.display.ToolPreviewLines != 2 {
		t.Fatalf("tool lines = %d, want 2", m.display.ToolPreviewLines)
	}
	execAll(cmd)
	after := m.transcript.Items()[0].Render(60)
	if after == before {
		t.Fatal("the card did not re-render after the tool height changed")
	}
	if strings.Count(after, "\n") >= strings.Count(before, "\n") {
		t.Fatalf("card did not shrink: before %q after %q", before, after)
	}
}

// SETU03: /display lines changes the value and writes display.thinking_lines to
// the user config.
func TestUI_SETU03_DisplayLinesPersists(t *testing.T) {
	m, _, _ := newModel(t)
	m.home = t.TempDir()
	m.setEditorText("/display lines 3")
	execAll(update(m, enterKey))
	if m.display.ThinkingPreviewLines != 3 {
		t.Fatalf("thinking lines = %d, want 3", m.display.ThinkingPreviewLines)
	}
	data, err := os.ReadFile(config.UserConfigPath(m.home) + ".yaml")
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	var doc struct {
		Display struct {
			ThinkingLines int `yaml:"thinking_lines"`
		} `yaml:"display"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("config not parseable: %v\n%s", err, data)
	}
	if doc.Display.ThinkingLines != 3 {
		t.Fatalf("saved thinking_lines = %d, want 3\n%s", doc.Display.ThinkingLines, data)
	}
}

// SETU05: a model built with display options starts from the saved values.
func TestUI_SETU05_SeededFromOptions(t *testing.T) {
	f := &fakeClient{responses: map[string]proto.Response{}}
	m := New(Options{
		Client: f, Theme: styles.Mono(), Clock: &fakeClock{now: time.Unix(1_700_000_000, 0)},
		CWD: t.TempDir(), Profile: colorprofile.ASCII, Renderer: echoRenderer{},
		Width: 80, Height: 24,
		Display: chat.Display{ThinkingPreviewLines: 12, ToolPreviewLines: 3, Splash: true},
	})
	if m.display.ThinkingPreviewLines != 12 || m.display.ToolPreviewLines != 3 {
		t.Fatalf("display = %+v, want {12 3}", m.display)
	}
}

// SETU06: /display lines rejects bad input with a notice and writes nothing,
// and clamps a huge value.
func TestUI_SETU06_DisplayLinesValidation(t *testing.T) {
	for _, v := range []string{"0", "-1", "abc"} {
		m, _, _ := newModel(t)
		m.home = t.TempDir()
		m.setEditorText("/display lines " + v)
		execAll(update(m, enterKey))
		if m.notice == "" {
			t.Fatalf("%q: expected a notice", v)
		}
		if _, err := os.Stat(config.UserConfigPath(m.home) + ".yaml"); !os.IsNotExist(err) {
			t.Fatalf("%q: a config was written", v)
		}
	}
	m, _, _ := newModel(t)
	m.home = t.TempDir()
	m.setEditorText("/display lines 999")
	execAll(update(m, enterKey))
	if m.display.ThinkingPreviewLines != 50 {
		t.Fatalf("huge value = %d, want 50", m.display.ThinkingPreviewLines)
	}
}

// SETU04: completion and help list the local settings commands.
func TestUI_SETU04_CommandsListed(t *testing.T) {
	m, _, _ := newModel(t)
	names := map[string]bool{}
	for _, c := range m.allCommands() {
		names[c.Name] = true
	}
	if !names["settings"] || !names["config"] {
		t.Fatalf("settings/config missing from commands: %v", names)
	}
}
