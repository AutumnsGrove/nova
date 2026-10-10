package mode

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
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
func testServer(t *testing.T, turns ...testkit.Turn) (*Server, *testkit.Provider) {
	t.Helper()
	cwd := t.TempDir()
	home := t.TempDir()
	prov := testkit.NewProvider(testkit.RealClock(), turns...)
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Tools.Active = []string{"read"}
	cfg.Compaction.Enabled = boolPtr(false)
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

func TestServer_UnknownCommand(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: "nope"})
	if resp.OK || resp.Error.Code != proto.CodeUnsupported {
		t.Fatalf("resp = %+v, want unsupported", resp)
	}
}

func TestServer_GetState_Fields(t *testing.T) {
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
