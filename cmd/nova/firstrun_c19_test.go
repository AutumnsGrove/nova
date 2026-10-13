package main

import (
	"bytes"
	"strings"
	"testing"
)

// runCLITerm calls run with an explicit terminal state and a buffer stdout.
func runCLITerm(t *testing.T, tty bool, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errOut, tty)
	return code, out.String(), errOut.String()
}

func TestFirstRun_FRS09_NonTUINoKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "")
	cases := [][]string{
		{"-p", "hi"},
		{"--mode", "json", "-p", "hi"},
		{"--mode", "rpc"},
	}
	for _, args := range cases {
		code, _, errOut := runCLITerm(t, true, args...)
		if code != 1 {
			t.Fatalf("%v: exit = %d, want 1", args, code)
		}
		if !strings.Contains(errOut, "no API key") {
			t.Fatalf("%v: stderr = %q, want the no-API-key message", args, errOut)
		}
	}
}

func TestFirstRun_FRS10_NoTerminalSkips(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "")
	_, out, _ := runCLITerm(t, false, "--mode", "tui")
	if strings.Contains(out, "Welcome") {
		t.Fatalf("the first-run flow ran without a terminal:\n%s", out)
	}
}
