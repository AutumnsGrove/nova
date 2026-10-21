package mode

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
)

// boolPtr returns a pointer to b.
func boolPtr(b bool) *bool { return &b }

// writeFile creates a file and its parents for tests.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// testServer builds a server with a scripted provider and a temp session.
// bundledNames lists the bundled extensions, so an older test can keep its
// server free of them and stay deterministic.
func bundledNames() []string { return []string{"tasks", "ask", "plan", "subagent", "webfetch"} }

func testServer(t *testing.T, turns ...testkit.Turn) (*Server, *testkit.Provider) {
	t.Helper()
	cwd := t.TempDir()
	home := t.TempDir()
	prov := testkit.NewProvider(testkit.RealClock(), turns...)
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Tools.Active = []string{"read"}
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	s, err := New(Options{
		Config:   &cfg,
		CWD:      cwd,
		Home:     home,
		Provider: prov,
		Session:  filepath.Join(t.TempDir(), "session.jsonl"),
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, prov
}

// collect drains events until run_end and returns them.
func collect(t *testing.T, events <-chan proto.Event) []proto.Event {
	t.Helper()
	var out []proto.Event
	for ev := range events {
		out = append(out, ev)
		if ev.Type == proto.EventRunEnd {
			return out
		}
	}
	return out
}

// waitFor reads events until typ arrives.
func waitFor(t *testing.T, events <-chan proto.Event, typ string) proto.Event {
	t.Helper()
	for ev := range events {
		if ev.Type == typ {
			return ev
		}
	}
	t.Fatalf("event %s was not seen", typ)
	return proto.Event{}
}

func TestPrint_PRI01_AnswerOnly(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "42"})
	var out, errOut bytes.Buffer
	code := RunPrint(context.Background(), s, "hi", &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if out.String() != "42\n" {
		t.Fatalf("stdout = %q, want %q", out.String(), "42\n")
	}
}

func TestPrint_PRI02_ProgressOnStderr(t *testing.T) {
	s, _ := testServer(t,
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "read", Args: `{"path":"hello.txt"}`}}},
		testkit.Turn{Text: "done"},
	)
	if err := os.WriteFile(filepath.Join(s.cwd, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := RunPrint(context.Background(), s, "read it", &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	if out.String() != "done\n" {
		t.Fatalf("stdout = %q, want only the answer", out.String())
	}
	if !bytes.Contains(errOut.Bytes(), []byte("read")) {
		t.Fatalf("stderr should mention the tool: %s", errOut.String())
	}
}

func TestServer_SRV02_EventsAroundRun(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "hello"})
	events, cancel := s.Subscribe(64)
	defer cancel()
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	if !resp.OK {
		t.Fatalf("prompt response = %+v, want ok", resp)
	}
	got := collect(t, events)
	if len(got) == 0 || got[0].Type != proto.EventRunStart {
		t.Fatalf("first event = %v, want run_start", got)
	}
	last := got[len(got)-1]
	if last.Type != proto.EventRunEnd || last.Reason != "done" {
		t.Fatalf("last event = %+v, want run_end done", last)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Seq <= got[i-1].Seq {
			t.Fatalf("seq is not increasing at %d: %d then %d", i, got[i-1].Seq, got[i].Seq)
		}
	}
}

func TestServer_SRV03_PromptWhileBusy(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	events, cancel := s.Subscribe(64)
	defer cancel()
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"}); !resp.OK {
		t.Fatalf("first prompt = %+v", resp)
	}
	// Wait for the run to start before the second prompt.
	waitFor(t, events, proto.EventRunStart)
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "two"}); resp.OK || resp.Error.Code != proto.CodeBusy {
		t.Fatalf("second prompt = %+v, want busy", resp)
	}
	s.agent.Abort()
}

func TestServer_SRV09_UnknownCommand(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: "nope"})
	if resp.OK || resp.Error.Code != proto.CodeUnsupported {
		t.Fatalf("resp = %+v, want unsupported", resp)
	}
}

func TestServer_SRV20_GetStateFields(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetState})
	if !resp.OK {
		t.Fatalf("get_state = %+v", resp)
	}
	for _, field := range []string{"model", "thinking", "context_limit", "session", "leaf", "running", "usage", "cwd"} {
		if !bytes.Contains(resp.Data, []byte(`"`+field+`"`)) {
			t.Errorf("get_state data missing %q: %s", field, resp.Data)
		}
	}
}

// TestServer_SRV65_MaxTokensReachesRequests checks the config's output cap on
// the loop's request and on a nested model call that names no cap of its own.
func TestServer_SRV65_MaxTokensReachesRequests(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	prov := testkit.NewProvider(testkit.RealClock(), testkit.Turn{Text: "ok"})
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.MaxTokens = 24
	cfg.Tools.Active = []string{"read"}
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	s, err := New(Options{
		Config: &cfg, CWD: cwd, Home: home, Provider: prov,
		Session: filepath.Join(t.TempDir(), "session.jsonl"), Version: "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"})
	collect(t, events)
	reqs := prov.Requests()
	if len(reqs) == 0 || reqs[0].MaxTokens != 24 {
		t.Fatalf("loop request MaxTokens = %d, want 24", reqs[0].MaxTokens)
	}

	// A nested call that asks for nothing inherits the cap; an explicit one
	// keeps its own value.
	nested := testkit.NewProvider(testkit.RealClock(), testkit.Turn{Text: "nested"}, testkit.Turn{Text: "nested"})
	s.mu.Lock()
	s.prov = nested
	s.mu.Unlock()
	if _, err := s.complete(context.Background(), provider.Request{Model: "m"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got := nested.Requests()[0].MaxTokens; got != 24 {
		t.Fatalf("nested MaxTokens = %d, want 24", got)
	}
	if _, err := s.complete(context.Background(), provider.Request{Model: "m", MaxTokens: 7}); err != nil {
		t.Fatalf("complete explicit: %v", err)
	}
	if got := nested.Requests()[1].MaxTokens; got != 7 {
		t.Fatalf("explicit MaxTokens = %d, want 7", got)
	}
}

// TestServer_SRV66_SubagentInheritsMaxTokens checks the server hands its output
// cap to the subagent spawner, so a child run is capped like its parent.
func TestServer_SRV66_SubagentInheritsMaxTokens(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	prov := testkit.NewProvider(testkit.RealClock(), testkit.Turn{Text: "final"})
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.MaxTokens = 24
	cfg.Tools.Active = []string{"read"}
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	s, err := New(Options{
		Config: &cfg, CWD: cwd, Home: home, Provider: prov,
		Session: filepath.Join(t.TempDir(), "session.jsonl"), Version: "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.spawn(context.Background(), ext.SpawnRequest{Task: "t"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if got := prov.Requests()[0].MaxTokens; got != 24 {
		t.Fatalf("child MaxTokens = %d, want 24", got)
	}
}

// TestServer_SRV67_SubagentKeepsRouting checks the server hands the session's
// catalog id to the spawner, so a child run stays on the session's provider.
func TestServer_SRV67_SubagentKeepsRouting(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	prov := testkit.NewProvider(testkit.RealClock(), testkit.Turn{Text: "final"})
	cfg := config.Defaults()
	cfg.Model = "opencode-go/deepseek-v4.1-flash"
	cfg.Tools.Active = []string{"read"}
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	s, err := New(Options{
		Config: &cfg, CWD: cwd, Home: home, Provider: prov,
		Session: filepath.Join(t.TempDir(), "session.jsonl"), Version: "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.spawn(context.Background(), ext.SpawnRequest{Task: "t"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if got := prov.Requests()[0].ModelID; got != "opencode-go/deepseek-v4.1-flash" {
		t.Fatalf("child ModelID = %q, want the session's catalog id", got)
	}
}
