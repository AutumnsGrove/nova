package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/testkit"
)

func TestCLI_CLI03_ConflictingModes(t *testing.T) {
	code, _, errOut := runCLI(t, "--cwd", t.TempDir(), "-p", "x", "--mode", "rpc")
	if code != 2 || errOut == "" {
		t.Fatalf("exit = %d stderr = %q, want a usage error", code, errOut)
	}
}

func TestCLI_CLI07_Logout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if code, _, errOut := runCLI(t, "logout"); code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
	if code, _, _ := runCLI(t, "logout"); code != 0 {
		t.Fatal("a second logout should also succeed")
	}
}

func TestCLI_CLI08_Models(t *testing.T) {
	code, out, _ := runCLI(t, "models")
	if code != 0 || !strings.Contains(out, "deepseek/deepseek-v4.1") {
		t.Fatalf("exit = %d out = %q", code, out)
	}
}

func TestCLI_CLI09_Sessions(t *testing.T) {
	cwd := t.TempDir()
	dir := session.DefaultDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, name := range []string{"alpha", "beta"} {
		st, err := session.Open(filepath.Join(dir, name+".jsonl"), session.Options{})
		if err != nil {
			t.Fatal(err)
		}
		msg := message.User(name)
		if _, err := st.Append(session.Entry{Type: session.TypeMessage, Message: &msg}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, st.Header().ID)
		_ = st.Close()
	}
	t.Chdir(cwd)
	code, out, _ := runCLI(t, "sessions")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, id := range ids {
		if !strings.Contains(out, id) {
			t.Fatalf("sessions output missing %s:\n%s", id, out)
		}
	}
}

func TestCLI_CLI10_Docs(t *testing.T) {
	code, out, _ := runCLI(t, "docs", "protocol")
	if code != 0 || !strings.Contains(out, "# Nova") {
		t.Fatalf("exit = %d out = %q", code, out)
	}
	code, out, _ = runCLI(t, "docs", "no-such-topic")
	if code != 0 || !strings.Contains(out, "topics:") {
		t.Fatalf("unknown topic should list topics: exit=%d out=%q", code, out)
	}
}

func TestCLI_CLI12_NoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	_, out, errOut := runCLI(t, "--help")
	if strings.Contains(out, "\x1b[") || strings.Contains(errOut, "\x1b[") {
		t.Fatal("NO_COLOR output should contain no ANSI escapes")
	}
}

func TestCLI_CLI13_ToolsFlags(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	withServer(t, srv)
	code, _, errOut := runCLI(t, "--cwd", t.TempDir(), "--no-session", "--tools", "read", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
	body := string(srv.Requests()[0].Body)
	if !strings.Contains(body, `"name":"read"`) || strings.Contains(body, `"name":"write"`) {
		t.Fatalf("--tools read should offer only read: %s", body)
	}

	srv2 := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	withServer(t, srv2)
	if code, _, _ := runCLI(t, "--cwd", t.TempDir(), "--no-session", "--no-tools", "-p", "hi"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if body := string(srv2.Requests()[0].Body); strings.Contains(body, `"tools"`) {
		t.Fatalf("--no-tools should send no tools: %s", body)
	}
}

func TestCLI_CLI14_Continue(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	withServer(t, srv)
	cwd := t.TempDir()
	dir := session.DefaultDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := session.Open(filepath.Join(dir, "prior.jsonl"), session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	msg := message.User("REMEMBER THIS")
	if _, err := st.Append(session.Entry{Type: session.TypeMessage, Message: &msg}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	code, _, errOut := runCLI(t, "--cwd", cwd, "--continue", "-p", "next")
	if code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
	if body := string(srv.Requests()[0].Body); !strings.Contains(body, "REMEMBER THIS") {
		t.Fatalf("--continue should carry the prior session: %s", body)
	}
}

func TestCLI_CLI15_SessionPath(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	withServer(t, srv)
	path := filepath.Join(t.TempDir(), "custom.jsonl")
	if code, _, errOut := runCLI(t, "--cwd", t.TempDir(), "--session", path, "-p", "hi"); code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("--session path was not created: %v", err)
	}
}

func TestCLI_CLI16_Completion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, out, _ := runCLI(t, "completion", shell)
		if code != 0 || out == "" {
			t.Fatalf("completion %s: exit = %d out = %q", shell, code, out)
		}
	}
}

func TestCLI_CLI18_DocsAfterDoubleDash(t *testing.T) {
	code, out, _ := runCLI(t, "docs", "--", "--help")
	if code != 0 || !strings.Contains(out, "topics:") {
		t.Fatalf("exit = %d out = %q", code, out)
	}
}

func TestCLI_FUL02_PolicyDeniesWrite(t *testing.T) {
	home := t.TempDir()
	writeArgs := `{"path":"out.txt","content":"x"}`
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Turn: testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "write", Args: writeArgs}}}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "done"}},
	)
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("NOVA_OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "nova"), 0o755); err != nil {
		t.Fatal(err)
	}
	configYAML := "permissions:\n  rules:\n    - tool: write\n      action: deny\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "nova", "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	code, _, _ := runCLI(t, "--cwd", cwd, "--no-session", "-p", "write it")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(cwd, "out.txt")); err == nil {
		t.Fatal("a denied write must not create the file")
	}
}

func TestCLI_FUL04_ResumeAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.jsonl")

	srv1 := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "one"}})
	withServer(t, srv1)
	if code, _, errOut := runCLI(t, "--cwd", t.TempDir(), "--session", path, "-p", "FIRST MESSAGE"); code != 0 {
		t.Fatalf("first run: exit = %d stderr = %q", code, errOut)
	}

	srv2 := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "two"}})
	withServer(t, srv2)
	if code, _, errOut := runCLI(t, "--cwd", t.TempDir(), "--session", path, "-p", "SECOND"); code != 0 {
		t.Fatalf("second run: exit = %d stderr = %q", code, errOut)
	}
	if body := string(srv2.Requests()[0].Body); !strings.Contains(body, "FIRST MESSAGE") {
		t.Fatalf("resumed run lost the first session's messages: %s", body)
	}
}

func TestCLI_FUL05_UntrustedProjectWarns(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}})
	withServer(t, srv)
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".nova"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".nova", "config.yaml"), []byte("model: sneaky/model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, "--cwd", cwd, "--no-session", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
	if !strings.Contains(errOut, "trust") {
		t.Fatalf("stderr should warn about the untrusted project: %q", errOut)
	}
	body := string(srv.Requests()[0].Body)
	if strings.Contains(body, "sneaky") {
		t.Fatalf("an untrusted project config must be ignored: %s", body)
	}
}

// logout removes the stored credential of the provider it names, OpenRouter's
// when none is named. It used to reach OpenRouter only, so an OpenCode Go key
// could not be removed from the command line.
func TestCLI_CLI19_LogoutNamesTheProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	keyPath := opencode.DefaultKeyPath(home)
	if err := opencode.SaveKey(keyPath, "go-key"); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCLI(t, "logout", "opencode-go"); code != 0 {
		t.Fatalf("logout opencode-go: exit %d stderr %q", code, errOut)
	}
	if _, err := os.Stat(keyPath); err == nil {
		t.Fatal("the OpenCode Go key survived its logout")
	}
	if code, _, _ := runCLI(t, "logout", "opencode-go"); code != 0 {
		t.Fatal("a second logout of the same provider should also succeed")
	}
	if code, _, errOut := runCLI(t, "logout", "nope"); code == 0 || !strings.Contains(errOut, "nope") {
		t.Fatalf("an unknown provider: exit %d stderr %q, want an error naming it", code, errOut)
	}
}
