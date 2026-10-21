package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

// runCLI calls run with a fresh stdin and returns the exit code and output.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return runCLIStdin(t, "", args...)
}

// runCLIStdin calls run with the given stdin.
func runCLIStdin(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut, true)
	return code, out.String(), errOut.String()
}

func TestCLI_CLI01_Help(t *testing.T) {
	code, out, _ := runCLI(t, "--help")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(strings.ToUpper(out), "USAGE") {
		t.Fatalf("stdout should carry usage:\n%s", out)
	}
}

func TestCLI_CLI02_UnknownFlag(t *testing.T) {
	code, _, errOut := runCLI(t, "--nope")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(strings.ToLower(errOut), "unknown flag") {
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

// withServer points the CLI at a testkit server and sets a dummy key and home.
func withServer(t *testing.T, srv *testkit.Server) {
	t.Helper()
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("NOVA_OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("HOME", t.TempDir())
}

func TestCLI_PRI03_PromptFromStdin(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	withServer(t, srv)
	code, out, errOut := runCLIStdin(t, "hello from stdin", "--cwd", t.TempDir(), "--no-session", "-p", "-")
	if code != 0 || out != "ok\n" {
		t.Fatalf("exit = %d out = %q stderr = %q", code, out, errOut)
	}
	if body := string(srv.Requests()[0].Body); !strings.Contains(body, "hello from stdin") {
		t.Fatalf("request did not carry the stdin prompt: %s", body)
	}
}

func TestCLI_PRI04_StdinAppendedFenced(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	withServer(t, srv)
	var out, errOut bytes.Buffer
	code := run([]string{"--cwd", t.TempDir(), "--no-session", "-p", "explain"}, strings.NewReader("PIPED CONTENT"), &out, &errOut, false)
	if code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, errOut.String())
	}
	body := string(srv.Requests()[0].Body)
	if !strings.Contains(body, "explain") || !strings.Contains(body, "PIPED CONTENT") || !strings.Contains(body, "```") {
		t.Fatalf("request missing the fenced stdin block: %s", body)
	}
}

func TestCLI_PRI05_EmptyPrompt(t *testing.T) {
	code, _, _ := runCLIStdin(t, "", "--cwd", t.TempDir(), "-p", "-")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestCLI_PRI07_Exhausted(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "read", Args: `{"path":"none"}`}}}})
	withServer(t, srv)
	code, _, _ := runCLI(t, "--cwd", t.TempDir(), "--no-session", "--max-turns", "1", "-p", "go")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (exhausted)", code)
	}
}

func TestCLI_PRI09_ToolErrorKeepsExitZero(t *testing.T) {
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "read", Args: `{"path":"does-not-exist"}`}}}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "recovered"}},
	)
	withServer(t, srv)
	code, out, errOut := runCLI(t, "--cwd", t.TempDir(), "--no-session", "-p", "go")
	if code != 0 || out != "recovered\n" {
		t.Fatalf("exit = %d out = %q stderr = %q", code, out, errOut)
	}
}

func TestCLI_PRI10_PermissionDeniedByHeadless(t *testing.T) {
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "write", Args: `{"path":"out.txt","content":"x"}`}}}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "done"}},
	)
	withServer(t, srv)
	cwd := t.TempDir()
	code, _, _ := runCLI(t, "--cwd", cwd, "--no-session", "-p", "write it")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(cwd, "out.txt")); err == nil {
		t.Fatal("write should have been denied without --yes")
	}
}

func TestCLI_PRI11_YesAllowsAndWarns(t *testing.T) {
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "write", Args: `{"path":"out.txt","content":"x"}`}}}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "done"}},
	)
	withServer(t, srv)
	cwd := t.TempDir()
	code, _, errOut := runCLI(t, "--cwd", cwd, "--no-session", "--yes", "-p", "write it")
	if code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
	if !strings.Contains(errOut, "--yes") {
		t.Fatalf("stderr should warn about --yes: %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(cwd, "out.txt")); err != nil {
		t.Fatalf("write should have run with --yes: %v", err)
	}
}

func TestCLI_JSN01_EventStream(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "42"}})
	withServer(t, srv)
	code, out, errOut := runCLI(t, "--cwd", t.TempDir(), "--no-session", "--mode", "json", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("want multiple JSONL events, got %q", out)
	}
	if !strings.Contains(lines[0], `"type":"run_start"`) || !strings.Contains(lines[len(lines)-1], `"type":"run_end"`) {
		t.Fatalf("first/last = %q / %q", lines[0], lines[len(lines)-1])
	}
	for _, line := range lines {
		var ev proto.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line is not a JSON event: %v (%s)", err, line)
		}
	}
}

func TestCLI_JSN02_NoOtherOutput(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "42"}})
	withServer(t, srv)
	code, out, _ := runCLI(t, "--cwd", t.TempDir(), "--no-session", "--mode", "json", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "{") {
			t.Fatalf("stdout has a non-frame line: %q", line)
		}
	}
}

func TestCLI_JSN03_ExitCodesMatchPrint(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Fault: testkit.Fault401})
	withServer(t, srv)
	code, _, _ := runCLI(t, "--cwd", t.TempDir(), "--no-session", "--mode", "json", "-p", "hi")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestCLI_JSN04_NoHello(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "42"}})
	withServer(t, srv)
	_, out, _ := runCLI(t, "--cwd", t.TempDir(), "--no-session", "--mode", "json", "-p", "hi")
	if strings.Contains(out, `"hello"`) {
		t.Fatalf("JSON mode must not write a hello frame: %s", out)
	}
}

// --resume opens the session picker, which only the interactive UI has. Without
// a terminal it is an error that says so, and the message must not claim the UI
// is missing from the build.
func TestCLI_RSM02_ResumeNeedsATerminal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	var out, errOut bytes.Buffer
	code := run([]string{"--cwd", t.TempDir(), "--resume"}, strings.NewReader(""), &out, &errOut, false)
	if code == 0 || !strings.Contains(errOut.String(), "--resume") || strings.Contains(errOut.String(), "not built") {
		t.Fatalf("exit %d, stderr %q, want a --resume error that does not say the UI is unbuilt", code, errOut.String())
	}
}
