//go:build !notui

package styles

import (
	"bytes"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

func TestStyles_STY01_DarkPalette(t *testing.T) {
	d := Dark()
	if d.Colors["spark"] != "#62c6ff" || d.Colors["ink"] != "#f2f6fb" ||
		d.Colors["core"] != "#fff4cf" || d.Colors["warm"] != "#ff9a5c" ||
		d.Colors["bg"] != "#0b0f16" {
		t.Fatalf("Dark does not use the brand palette: %+v", d.Colors)
	}
	got := Build(d, colorprofile.TrueColor).Spark.Render("x")
	if !strings.Contains(got, "38;2;98;198;255") {
		t.Fatalf("spark is not rendered as the brand truecolor: %q", got)
	}
}

func TestStyles_STY02_NoColourLiterals(t *testing.T) {
	hex := regexp.MustCompile(`#[0-9a-fA-F]{6}`)
	lit := regexp.MustCompile(`Color\("`)
	for _, name := range []string{"styles.go", "profile.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(data)
		if loc := hex.FindString(src); loc != "" {
			t.Fatalf("%s contains a colour literal %q; colours belong in theme.go", name, loc)
		}
		if loc := lit.FindString(src); loc != "" {
			t.Fatalf("%s contains %q; colours belong in theme.go", name, loc)
		}
	}
}

func TestStyles_STY03_MonoEmitsNoColour(t *testing.T) {
	s := Build(Mono(), colorprofile.TrueColor)
	for name, style := range sample(s) {
		got := style.Render("sample")
		if strings.Contains(got, "38;") || strings.Contains(got, "48;") {
			t.Fatalf("%s emitted a colour escape under Mono: %q", name, got)
		}
	}
}

func TestStyles_STY04_NoColorEnv(t *testing.T) {
	env := func(k string) string {
		if k == "NO_COLOR" {
			return "1"
		}
		return ""
	}
	if p := DetectProfile(env, &bytes.Buffer{}); p != colorprofile.ASCII {
		t.Fatalf("NO_COLOR gave profile %v, want no colour", p)
	}
}

func TestStyles_STY05_NonTerminalOutput(t *testing.T) {
	env := func(string) string { return "" }
	if p := DetectProfile(env, &bytes.Buffer{}); p > colorprofile.ASCII {
		t.Fatalf("non-terminal gave profile %v, want no colour", p)
	}
}

func TestStyles_STY06_DegradesColour(t *testing.T) {
	for _, p := range []colorprofile.Profile{colorprofile.ANSI256, colorprofile.ANSI} {
		got := Build(Dark(), p).Spark.Render("x")
		if strings.Contains(got, "38;2;") || strings.Contains(got, "48;2;") {
			t.Fatalf("profile %v emitted a truecolor escape: %q", p, got)
		}
		if !strings.Contains(got, "\x1b[") {
			t.Fatalf("profile %v emitted no colour at all: %q", p, got)
		}
	}
}

func TestStyles_STY07_LightThemeContrast(t *testing.T) {
	l := Light()
	ratio := contrast(l.Colors["ink"], l.Colors["bg"])
	if ratio < 4.5 {
		t.Fatalf("light body text contrast is %.2f, want at least 4.5", ratio)
	}
}

func TestStyles_STY08_ToolPanelBackground(t *testing.T) {
	d := Build(Dark(), colorprofile.TrueColor)
	panels := map[string]lipgloss.Style{
		"pending": d.ToolPanel,
		"success": d.ToolPanelOK,
		"error":   d.ToolPanelError,
		"user":    d.UserPanel,
	}
	seen := map[string]bool{}
	for name, s := range panels {
		got := s.Render("x")
		if !strings.Contains(got, "48;") {
			t.Fatalf("%s panel has no background: %q", name, got)
		}
		if seen[got] {
			t.Fatalf("%s panel shares a colour with another state", name)
		}
		seen[got] = true
	}
	if got := Build(Mono(), colorprofile.TrueColor).ToolPanel.Render("x"); strings.Contains(got, "48;") {
		t.Fatalf("mono panel emitted a background: %q", got)
	}
}

// sample returns every style by name so a test can check them all.
func sample(s Styles) map[string]interface{ Render(...string) string } {
	return map[string]interface{ Render(...string) string }{
		"Header": s.Header, "Footer": s.Footer, "Text": s.Text, "Muted": s.Muted,
		"Spark": s.Spark, "Core": s.Core, "Warm": s.Warm, "Success": s.Success,
		"Error": s.Error, "User": s.User, "Assistant": s.Assistant, "Thinking": s.Thinking,
		"Tool": s.Tool, "ToolError": s.ToolError, "Editor": s.Editor, "DiffAdd": s.DiffAdd,
		"ToolPanel": s.ToolPanel, "ToolPanelOK": s.ToolPanelOK, "ToolPanelError": s.ToolPanelError, "UserPanel": s.UserPanel,
	}
}

// contrast returns the WCAG contrast ratio between two hex colours.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// luminance returns the WCAG relative luminance of a hex colour.
func luminance(hex string) float64 {
	r, g, b := parseHex(hex)
	lin := func(c float64) float64 {
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func parseHex(hex string) (float64, float64, float64) {
	hex = strings.TrimPrefix(hex, "#")
	n, _ := strconv.ParseUint(hex, 16, 32)
	return float64((n>>16)&0xff) / 255, float64((n>>8)&0xff) / 255, float64(n&0xff) / 255
}
