//go:build !notui

package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/tui/render"
	"github.com/AutumnsGrove/nova/tui/styles"
)

// colorStyles is the panel tests' colourful theme; the rest of chat's tests are
// mono so they do not see backgrounds.
func colorStyles() styles.Styles { return styles.Build(styles.Dark(), colorprofile.TrueColor) }

// PNL01: every line of a card carries the panel background for its state, the
// three states differ, and each line resets so the panel cannot bleed.
func TestChat_PNL01_StateBackground(t *testing.T) {
	st := colorStyles()
	card := "● read a.go\nresult line"
	mk := func(c render.Call) string { return Panel(card, c, st, 40) }
	running, ok, failed := mk(render.Call{Running: true}), mk(render.Call{}), mk(render.Call{IsError: true})
	for name, out := range map[string]string{"running": running, "ok": ok, "error": failed} {
		if out == "" {
			t.Fatalf("%s: empty panel", name)
		}
		for _, l := range strings.Split(out, "\n") {
			if !strings.HasPrefix(l, "\x1b[48;") {
				t.Fatalf("%s: line does not open with a background: %q", name, l)
			}
			if !strings.HasSuffix(l, ansi.ResetStyle) {
				t.Fatalf("%s: line does not reset: %q", name, l)
			}
		}
	}
	if running == ok || ok == failed || running == failed {
		t.Fatal("the three card states share a background")
	}
}

// PNL02: the panel spans the full width with one cell of padding on each side.
func TestChat_PNL02_PaddingAndWidth(t *testing.T) {
	st := colorStyles()
	for width := 20; width <= 200; width++ {
		out := Panel("● bash ls -la", render.Call{}, st, width)
		for _, l := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(l); w != width {
				t.Fatalf("width %d: line %q is %d wide", width, l, w)
			}
			plain := ansi.Strip(l)
			if plain[0] != ' ' || plain[len(plain)-1] != ' ' {
				t.Fatalf("width %d: padding missing: %q", width, plain)
			}
		}
	}
}

// PNL03: Mono and a colourless profile emit no background and still pad.
func TestChat_PNL03_MonoNoBackground(t *testing.T) {
	out := Panel("● read a.go\nresult", render.Call{}, testStyles(), 40)
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "\x1b[") {
			t.Fatalf("mono panel emitted an escape: %q", l)
		}
		if w := ansi.StringWidth(l); w != 40 {
			t.Fatalf("mono panel line is %d wide, want 40", w)
		}
	}
}
