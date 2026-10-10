package mode

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/openrouter"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/testkit"
)

func TestServer_SRV11_HandlerPanicRecovered(t *testing.T) {
	s, _ := testServer(t)
	resp := s.recoverResponse(proto.Command{Type: proto.CmdGetState}, func() proto.Response {
		panic("boom")
	})
	if resp.OK || resp.Error.Code != proto.CodeInternal {
		t.Fatalf("panic response = %+v, want internal", resp)
	}
	if r := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetState}); !r.OK {
		t.Fatalf("server should keep serving after a panic: %+v", r)
	}
}

func TestServer_SRV15_PersistenceError(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "x"})
	_ = s.store.Close()
	events, cancel := s.Subscribe(64)
	defer cancel()
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"}); !resp.OK {
		t.Fatalf("prompt = %+v", resp)
	}
	got := collect(t, events)
	if last := got[len(got)-1]; last.Type != proto.EventRunEnd || last.Reason != "error" {
		t.Fatalf("last event = %+v, want run_end error", last)
	}
}

func TestServer_SRV17_CompactionTriggers(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "summary"}, testkit.Turn{Text: "final"})
	s.cfg.ContextLimit = 40
	s.limit = 40
	s.compactor.Settings.Enabled = true
	s.compactor.Settings.KeepRecent = 1
	long := strings.Repeat("word ", 200)
	if _, err := s.store.Append(session.Entry{Type: session.TypeMessage, Message: msgPtr(message.User(long))}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Append(session.Entry{Type: session.TypeMessage, Message: msgPtr(message.Assistant(long))}); err != nil {
		t.Fatal(err)
	}
	events, cancel := s.Subscribe(256)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"})
	got := collect(t, events)
	sawCompaction := false
	for _, ev := range got {
		if ev.Type == proto.EventCompaction {
			sawCompaction = true
		}
	}
	if !sawCompaction {
		t.Fatalf("expected a compaction event, got %d events", len(got))
	}
	if last := got[len(got)-1]; last.Reason != "done" {
		t.Fatalf("reason = %q, want done", last.Reason)
	}
}

func TestServer_SRV18_CompactionFailure(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Err: errors.New("summariser down")}, testkit.Turn{Text: "final"})
	s.cfg.ContextLimit = 40
	s.limit = 40
	s.compactor.Settings.Enabled = true
	s.compactor.Settings.KeepRecent = 1
	long := strings.Repeat("word ", 200)
	_, _ = s.store.Append(session.Entry{Type: session.TypeMessage, Message: msgPtr(message.User(long))})
	_, _ = s.store.Append(session.Entry{Type: session.TypeMessage, Message: msgPtr(message.Assistant(long))})
	events, cancel := s.Subscribe(256)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"})
	got := collect(t, events)
	sawError := false
	for _, ev := range got {
		if ev.Type == proto.EventError && strings.Contains(ev.Error.Message, "compaction failed") {
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("expected a compaction failure error, got %d events", len(got))
	}
	if last := got[len(got)-1]; last.Reason != "done" {
		t.Fatalf("a compaction failure should not end the run: %+v", last)
	}
}

func TestServer_SRV22_SetModelBlank(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSetModel, Model: "   "})
	if resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("set_model = %+v, want bad_request", resp)
	}
}

func TestServer_SRV23_SetTools(t *testing.T) {
	s, prov := testServer(t, testkit.Turn{Text: "ok"})
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSetTools, Active: []string{"read"}}); !resp.OK {
		t.Fatalf("set_tools = %+v", resp)
	}
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	collect(t, events)
	reqs := prov.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request recorded")
	}
	names := make([]string, len(reqs[0].Tools))
	for i, def := range reqs[0].Tools {
		names[i] = def.Function.Name
	}
	if len(names) != 1 || names[0] != "read" {
		t.Fatalf("tools = %v, want [read]", names)
	}
}

func TestServer_SRV24_SessionResumeAndFork(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "hi"})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	collect(t, events)

	first := s.store.Entries()
	if len(first) == 0 {
		t.Fatal("no entries")
	}
	forkResp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionFork, Entry: first[0].ID})
	if !forkResp.OK {
		t.Fatalf("session.fork = %+v", forkResp)
	}
	if len(s.store.Entries()) == 0 {
		t.Fatal("forked session should carry the branch")
	}
	forkID := s.sessionID()
	if newResp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionNew}); !newResp.OK {
		t.Fatalf("session.new = %+v", newResp)
	}
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionResume, Session: forkID}); !resp.OK {
		t.Fatalf("session.resume = %+v", resp)
	}
}

func TestServer_SRV25_SessionCommandsBusy(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	waitFor(t, events, proto.EventRunStart)
	for _, cmd := range []proto.Command{
		{Type: proto.CmdSessionNew},
		{Type: proto.CmdSessionResume, Session: "x"},
		{Type: proto.CmdSessionFork, Entry: "x"},
		{Type: proto.CmdSessionNavigate, Entry: "x"},
	} {
		if resp := s.Handle(context.Background(), cmd); resp.OK || resp.Error.Code != proto.CodeBusy {
			t.Fatalf("%s while busy = %+v, want busy", cmd.Type, resp)
		}
	}
	s.agent.Abort()
}

func TestServer_SRV26_NavigateThenBranch(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "one"}, testkit.Turn{Text: "two"})
	events, cancel := s.Subscribe(128)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "first"})
	collect(t, events)
	entries := s.store.Entries()
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
	target := entries[0].ID
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionNavigate, Entry: target}); !resp.OK {
		t.Fatalf("navigate = %+v", resp)
	}
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "branch"})
	collect(t, events)
	found := false
	for _, e := range s.store.Entries() {
		if e.Parent == target {
			found = true
		}
	}
	if !found {
		t.Fatalf("no entry branch parented to the navigated entry %q", target)
	}
}

func TestServer_SRV27_ReloadPicksUpSkill(t *testing.T) {
	s, prov := testServer(t, testkit.Turn{Text: "ok"})
	if err := writeFile(s.cwd+"/.nova/skills/foo/SKILL.md", "---\nname: foo\ndescription: fresh skill\n---\nbody"); err != nil {
		t.Fatal(err)
	}
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdReload}); !resp.OK {
		t.Fatalf("reload = %+v", resp)
	}
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	collect(t, events)
	reqs := prov.Requests()
	if len(reqs) == 0 || len(reqs[0].Messages) == 0 {
		t.Fatal("no request")
	}
	if system := reqs[0].Messages[0].Content; !strings.Contains(system, "fresh skill") {
		t.Fatalf("reloaded system prompt missing the skill:\n%s", system)
	}
}

func TestServer_SRV28_ReloadWaitsForRun(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	waitFor(t, events, proto.EventRunStart)
	done := make(chan proto.Response, 1)
	go func() { done <- s.Handle(context.Background(), proto.Command{Type: proto.CmdReload}) }()
	select {
	case <-done:
		t.Fatal("reload should wait for the active run")
	case <-time.After(30 * time.Millisecond):
	}
	s.agent.Abort()
	select {
	case resp := <-done:
		if !resp.OK {
			t.Fatalf("reload after abort = %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reload did not return after the run ended")
	}
}

func TestUI_SRV35_TimeoutDefault(t *testing.T) {
	s, _ := testServer(t)
	ms := 5
	resp, err := s.callUI(context.Background(), proto.UIRequest{
		Type: proto.FrameUIRequest, ID: "u1", Method: "ui.confirm", TimeoutMS: &ms, Default: boolPtr(true),
	})
	if err != nil || !resp.OK {
		t.Fatalf("callUI = %+v, %v; want the default (true)", resp, err)
	}
}

func TestUI_SRV43_ExpiryEvent(t *testing.T) {
	s, _ := testServer(t)
	events, cancel := s.Subscribe(64)
	defer cancel()
	ms := 5
	if _, err := s.callUI(context.Background(), proto.UIRequest{
		Type: proto.FrameUIRequest, ID: "u2", Method: "ui.confirm", TimeoutMS: &ms, Default: boolPtr(false),
	}); err != nil {
		t.Fatalf("callUI: %v", err)
	}
	for {
		select {
		case ev := <-events:
			if ev.Type == proto.EventUIExpired && ev.ID == "u2" {
				return
			}
		default:
			t.Fatal("expected a ui_expired event with the id")
		}
	}
}

func TestServer_SRV37_ShutdownMidRun(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	waitFor(t, events, proto.EventRunStart)
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdShutdown}); !resp.OK {
		t.Fatalf("shutdown = %+v", resp)
	}
}

func TestServer_SRV38_CloseTwice(t *testing.T) {
	s, _ := testServer(t)
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestServer_SRV39_ProviderError(t *testing.T) {
	srv := testkit.NewServer(t, testkit.ServerTurn{Fault: testkit.Fault401})
	s := serverWithHTTP(t, srv.URL)
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	got := collect(t, events)
	sawProvider := false
	for _, ev := range got {
		if ev.Type == proto.EventError && ev.Error.Code == proto.CodeProviderError {
			sawProvider = true
		}
	}
	if !sawProvider || got[len(got)-1].Reason != "error" {
		t.Fatalf("events = %+v, want provider_error and run_end(error)", got)
	}
}

func TestServer_SRV40_RetryVisible(t *testing.T) {
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Fault: testkit.Fault500},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "ok"}},
	)
	s := serverWithHTTP(t, srv.URL)
	events, cancel := s.Subscribe(256)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	got := collect(t, events)
	sawRetry := false
	for _, ev := range got {
		if ev.Type == proto.EventRetry {
			sawRetry = true
		}
	}
	if !sawRetry || got[len(got)-1].Reason != "done" {
		t.Fatalf("events did not show a retry then success: %+v", got)
	}
}

func TestServer_SRV41_UsageTotals(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "x", Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	got := collect(t, events)
	var total *provider.Usage
	for _, ev := range got {
		if ev.Type == proto.EventRunEnd {
			total = ev.Usage
		}
	}
	if total == nil || total.TotalTokens != 5 {
		t.Fatalf("run_end usage = %+v, want 5 tokens", total)
	}
}

func TestServer_SRV42_ThinkingEvents(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Reasoning: "let me think", Text: "x"})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	got := collect(t, events)
	kinds := map[string]bool{}
	for _, ev := range got {
		kinds[ev.Type] = true
	}
	for _, want := range []string{proto.EventThinkingStart, proto.EventThinkingDelta, proto.EventThinkingEnd} {
		if !kinds[want] {
			t.Fatalf("missing %s in %+v", want, kinds)
		}
	}
}

// serverWithHTTP builds a mode server pointed at a local SSE test server.
func serverWithHTTP(t *testing.T, baseURL string) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.Tools.Active = []string{"read", "bash"}
	cfg.Tools.Bash.MaxOutputBytes = 2 << 20
	cfg.Permissions.Headless = permission.Allow
	cfg.Retry.BaseMS = 1
	cfg.Retry.CapMS = 5
	p := openrouter.New(openrouter.Config{BaseURL: baseURL, APIKey: "test-key"})
	s, err := New(Options{
		Config:   &cfg,
		CWD:      t.TempDir(),
		Home:     t.TempDir(),
		Provider: p,
		Session:  t.TempDir() + "/s.jsonl",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// msgPtr returns a pointer to m.
func msgPtr(m message.Message) *message.Message { return &m }
