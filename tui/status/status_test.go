//go:build !notui

package status

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/tui/styles"
)

func tc() styles.Styles { return styles.Build(styles.Dark(), colorprofile.TrueColor) }

func sample() Data {
	return Data{
		CWD: "~/Projects/nova", Branch: "main", GitOK: true, Added: 1, Removed: 2,
		Model: "sonnet", Provider: "openrouter", Thinking: "off",
		Prompt: 12000, Completion: 4000, Cached: 9000, Cost: 0.02,
		ContextTokens: 34, ContextLimit: 100,
		Session: 14 * time.Minute, Block: 5 * time.Minute,
		Queued: 1, Hint: "ctrl+o expand",
	}
}

func TestStatus_SLN01_DefaultLayout(t *testing.T) {
	out := Build(Default(), sample(), 100, tc(), nil)
	if len(out) != 3 {
		t.Fatalf("lines = %d, want 3:\n%s", len(out), strings.Join(out, "\n"))
	}
	joined := strings.Join(out, "\n")
	for _, want := range []string{"~/Projects/nova", "main", "(+1,-2)", "↑12k", "↓4k", "R9k", "CH75.0%", "$0.020", "34%/100", "(auto)", "session 14m", "block 5m", "1 queued", "ctrl+o expand", "sonnet", "thinking off"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q:\n%s", want, joined)
		}
	}
}

func TestStatus_SLN02_ModelIsRightAligned(t *testing.T) {
	out := Build(Default(), sample(), 100, tc(), nil)
	last := out[2]
	if got := ansi.StringWidth(last); got != 100 {
		t.Fatalf("last line width = %d, want 100", got)
	}
	if idx := strings.Index(last, "sonnet"); idx < 60 {
		t.Fatalf("model is not right-aligned: %q", ansi.Strip(last))
	}
}

func TestStatus_SLN03_NarrowDropsRightGroup(t *testing.T) {
	out := Build(Default(), sample(), 30, tc(), nil)
	for _, line := range out {
		if w := ansi.StringWidth(line); w > 30 {
			t.Fatalf("line is %d wide, want <= 30: %q", w, ansi.Strip(line))
		}
	}
	if strings.Contains(ansi.Strip(out[2]), "sonnet") {
		t.Fatalf("the right group should be dropped before the left truncates: %q", ansi.Strip(out[2]))
	}
}

func TestStatus_SLN04_ContextPressure(t *testing.T) {
	st := tc()
	cases := []struct {
		tokens int
		want   string
	}{
		{40, "text"},
		{71, "warn"},
		{91, "error"},
	}
	for _, c := range cases {
		d := sample()
		d.ContextTokens = c.tokens
		if got := resolve("ctx", d, st).Style; got != c.want {
			t.Fatalf("ctx at %d%% = %q, want %q", c.tokens, got, c.want)
		}
	}
}

func TestStatus_SLN05_ExtensionWrapping(t *testing.T) {
	ext := []Ext{
		{Text: "go 5h 0.3%", Style: "muted"},
		{Text: "parallel 92/4,000", Style: "accent"},
		{Text: "semsearch: unavailable", Style: "warn"},
	}
	out := Build(Default(), sample(), 30, tc(), ext)
	if len(out) != 5 {
		t.Fatalf("lines = %d, want the 3 configured plus 2 extension rows:\n%s", len(out), strings.Join(out, "\n"))
	}
	if !strings.Contains(ansi.Strip(out[3]+out[4]), "parallel") {
		t.Fatalf("extension text missing:\n%s", strings.Join(out, "\n"))
	}
	for _, line := range out {
		if w := ansi.StringWidth(line); w > 30 {
			t.Fatalf("line is %d wide: %q", w, ansi.Strip(line))
		}
	}
}

func TestStatus_SLN06_Height(t *testing.T) {
	if got := Height(Default(), true); got != 5 {
		t.Fatalf("default with ext = %d, want 5", got)
	}
	if got := Height(Default(), false); got != 3 {
		t.Fatalf("default without ext = %d, want 3", got)
	}
	if got := Height(Compact(), false); got != 1 {
		t.Fatalf("compact = %d, want 1", got)
	}
	if got := Height(config.StatusConfig{}, false); got != 3 {
		t.Fatalf("zero config = %d, want the 3 default lines", got)
	}
}

func TestStatus_SLN07_EmptyValuesAreOmitted(t *testing.T) {
	d := Data{CWD: "~/x", Session: time.Minute}
	out := Build(Default(), d, 100, tc(), nil)
	joined := ansi.Strip(strings.Join(out, "\n"))
	for _, not := range []string{"main", "(+", "↑", "↓", "R9", "CH", "1 queued", "sonnet"} {
		if strings.Contains(joined, not) {
			t.Fatalf("unexpected %q with empty data:\n%s", not, joined)
		}
	}
	if !strings.Contains(joined, "ctx n/a") {
		t.Fatalf("missing the deliberate ctx n/a state:\n%s", joined)
	}
}

func TestStatus_SLN08_MonoHasNoColour(t *testing.T) {
	mono := styles.Build(styles.Mono(), colorprofile.ASCII)
	out := Build(Default(), sample(), 100, mono, []Ext{{Text: "go", Style: "accent"}})
	for _, line := range out {
		if strings.Contains(line, "\x1b[") {
			t.Fatalf("mono status has an escape: %q", line)
		}
	}
}

func TestStatus_SLN09_NeverExceedsWidth(t *testing.T) {
	ext := []Ext{{Text: "a very long extension status that must wrap somewhere", Style: "muted"}}
	for w := 10; w <= 200; w += 7 {
		out := Build(Default(), sample(), w, tc(), ext)
		for _, line := range out {
			if got := ansi.StringWidth(line); got > w {
				t.Fatalf("width %d: line is %d wide: %q", w, got, ansi.Strip(line))
			}
		}
	}
}

func TestStatus_SLN10_EmptySeparatorFallsBack(t *testing.T) {
	cfg := Default()
	cfg.Separator = ""
	out := Build(cfg, sample(), 100, tc(), nil)
	if !strings.Contains(ansi.Strip(out[0]), " · ") {
		t.Fatalf("default separator missing: %q", ansi.Strip(out[0]))
	}
}

func TestStatus_STS03_StyleMapping(t *testing.T) {
	st := tc()
	if styleFor("warn", st).Render("x") == styleFor("error", st).Render("x") {
		t.Fatal("warn and error should map to different theme tokens")
	}
	if styleFor("nope", st).Render("x") != styleFor("muted", st).Render("x") {
		t.Fatal("an unknown style should fall back to muted")
	}
	if styleFor("", st).Render("x") != styleFor("muted", st).Render("x") {
		t.Fatal("an empty style should map to muted")
	}
}
