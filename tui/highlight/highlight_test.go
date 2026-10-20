//go:build !notui

package highlight

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHighlight_HLT01_ReturnsOneLinePerInputLine(t *testing.T) {
	code := "package main\n\nfunc main() {}\n"
	for _, color := range []bool{false, true} {
		if got, want := len(Lines(code, "main.go", color)), len(strings.Split(code, "\n")); got != want {
			t.Errorf("color=%v: %d lines, want %d", color, got, want)
		}
	}
}

// Highlighting is how a file's bytes reach the screen, and the file is whatever
// the model or a repository holds, so colour must not let a control sequence
// ride through inside the token text.
func TestHighlight_HLT02_FileEscapesNeverReachTheOutput(t *testing.T) {
	code := "x := \"a\x1b]0;pwned\x07b\x1b[2Jc‮d\"\nreturn\r\n"
	for _, color := range []bool{false, true} {
		out := strings.Join(Lines(code, "x.go", color), "\n")
		if strings.Contains(out, "\x1b]") || strings.Contains(out, "[2J") || strings.ContainsAny(out, "\x07\r‮") {
			t.Errorf("color=%v: control bytes survived: %q", color, out)
		}
		if !strings.Contains(ansi.Strip(out), "abcd") {
			t.Errorf("color=%v: the visible text was lost: %q", color, out)
		}
	}
}

func TestHighlight_HLT03_LineIsTheFirstOfLines(t *testing.T) {
	if got := Line("a := 1", "x.go", false); got != "a := 1" {
		t.Fatalf("Line = %q, want the input unchanged without colour", got)
	}
}
