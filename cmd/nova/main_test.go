package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/testkit"
)

// runCLI calls run with a fresh stdin and returns the exit code and output.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errOut, true)
	return code, out.String(), errOut.String()
}

func TestCLI_CLI01_Help(t *testing.T) {
	code, out, _ := runCLI(t, "--help")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "Usage:") {
		t.Fatalf("stdout should carry usage:\n%s", out)
	}
}

func TestCLI_CLI02_UnknownFlag(t *testing.T) {
	code, _, errOut := runCLI(t, "--nope")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown flag") {
		t.Fatalf("stderr should name the flag:\n%s", errOut)
	}
}

func TestCLI_CLI04_Version(t *testing.T) {
	code, out, _ := runCLI(t, "--version")
	if code != 0 || !strings.Contains(out, "dev") {
		t.Fatalf("exit = %d out = %q, want dev", code, out)
	}
}

func TestCLI_CLI05_CwdMissing(t *testing.T) {
	code, _, errOut := runCLI(t, "--cwd", "/no/such/dir/nova", "-p", "x")
	if code != 2 || !strings.Contains(errOut, "--cwd") {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
}

func TestCLI_CLI11_NoMode(t *testing.T) {
	code, _, errOut := runCLI(t)
	if code != 2 || !strings.Contains(errOut, "no mode") {
		t.Fatalf("exit = %d stderr = %q, want a usage error", code, errOut)
	}
}

func TestCLI_CLI17_UnknownSubcommand(t *testing.T) {
	code, _, errOut := runCLI(t, "nope")
	if code != 2 || errOut == "" {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
}

func TestCLI_PRI01_PrintEndToEnd(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "42"}})
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("NOVA_OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("HOME", t.TempDir())
	code, out, errOut := runCLI(t, "--cwd", t.TempDir(), "--no-session", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if out != "42\n" {
		t.Fatalf("stdout = %q, want %q", out, "42\n")
	}
}

func TestCLI_PRI06_ProviderFailure(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Fault: testkit.Fault401})
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("NOVA_OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("HOME", t.TempDir())
	code, out, errOut := runCLI(t, "--cwd", t.TempDir(), "--no-session", "-p", "hi")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errOut)
	}
	if errOut == "" {
		t.Fatal("a provider failure should be reported on stderr")
	}
	_ = out
}

func TestCLI_PRI02_ToolProgressOnStderr(t *testing.T) {
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "read", Args: `{"path":"f.txt"}`}}}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "done"}},
	)
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("NOVA_OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, "--cwd", cwd, "--no-session", "-p", "read it")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if out != "done\n" {
		t.Fatalf("stdout = %q, want only the answer", out)
	}
	if !strings.Contains(errOut, "read") {
		t.Fatalf("stderr should carry tool progress: %q", errOut)
	}
}

func TestCLI_FUL01_ReadEditAnswer(t *testing.T) {
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "read", Args: `{"path":"f.txt"}`}}}},
		testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{{ID: "c2", Name: "edit", Args: `{"path":"f.txt","edits":[{"oldText":"old","newText":"new"}]}`}}}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "changed"}},
	)
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("NOVA_OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	path := filepath.Join(cwd, "f.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, "--cwd", cwd, "--no-session", "--yes", "-p", "edit it")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if out != "changed\n" {
		t.Fatalf("stdout = %q", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new") {
		t.Fatalf("file was not edited: %q", data)
	}
}
