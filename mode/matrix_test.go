package mode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/testkit"
)

// testServerNoKey builds a server with the real OpenRouter wiring and no key.
func testServerNoKey(t *testing.T) *Server {
	t.Helper()
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("HOME", home)
	cfg := config.Defaults()
	cfg.Tools.Active = []string{"read"}
	s, err := New(Options{
		Config:  &cfg,
		CWD:     cwd,
		Home:    home,
		Session: filepath.Join(t.TempDir(), "s.jsonl"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestServer_SRV01_NoKey(t *testing.T) {
	s := testServerNoKey(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	if resp.OK || resp.Error.Code != proto.CodeProviderError || !strings.Contains(resp.Error.Message, "nova login") {
		t.Fatalf("prompt = %+v, want provider_error with login hint", resp)
	}
}

func TestServer_SRV04_QueueWhileBusy(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	waitFor(t, events, proto.EventRunStart)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "two", Queue: true})
	if !resp.OK {
		t.Fatalf("queued prompt = %+v", resp)
	}
	if _, followUp := s.agent.Queued(); followUp != 1 {
		t.Fatalf("follow_up = %d, want 1", followUp)
	}
	s.agent.Abort()
}

func TestServer_SRV05_SteerDuringRun(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	waitFor(t, events, proto.EventRunStart)
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSteer, Text: "be brief"}); !resp.OK {
		t.Fatalf("steer = %+v", resp)
	}
	if steering, _ := s.agent.Queued(); steering != 1 {
		t.Fatalf("steering = %d, want 1", steering)
	}
	s.agent.Abort()
}

func TestServer_SRV06_IdleSteerAndFollowUp(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "done"})
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSteer, Text: "x"}); resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("idle steer = %+v, want bad_request", resp)
	}
	events, cancel := s.Subscribe(64)
	defer cancel()
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdFollowUp, Text: "go"}); !resp.OK {
		t.Fatalf("idle follow_up = %+v", resp)
	}
	got := collect(t, events)
	if got[len(got)-1].Reason != "done" {
		t.Fatalf("follow_up run ended %q, want done", got[len(got)-1].Reason)
	}
}

func TestServer_SRV07_Abort(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	waitFor(t, events, proto.EventRunStart)
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdAbort}); !resp.OK {
		t.Fatalf("abort = %+v", resp)
	}
	for ev := range events {
		if ev.Type == proto.EventRunEnd {
			if ev.Reason != "aborted" {
				t.Fatalf("reason = %q, want aborted", ev.Reason)
			}
			return
		}
	}
	t.Fatal("no run_end")
}

func TestServer_SRV08_AbortIdle(t *testing.T) {
	s, _ := testServer(t)
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdAbort}); !resp.OK {
		t.Fatalf("idle abort = %+v", resp)
	}
}

func TestServer_SRV10_SetModelMissing(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSetModel})
	if resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("set_model = %+v, want bad_request", resp)
	}
}

func TestServer_SRV12_TwoSubscribersInOrder(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "hi"})
	a, cancelA := s.Subscribe(64)
	defer cancelA()
	b, cancelB := s.Subscribe(64)
	defer cancelB()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	first := collect(t, a)
	second := collect(t, b)
	if len(first) != len(second) {
		t.Fatalf("subscriber lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Type != second[i].Type {
			t.Fatalf("event %d differs: %s vs %s", i, first[i].Type, second[i].Type)
		}
		if i > 0 && second[i].Seq <= second[i-1].Seq {
			t.Fatalf("seq not increasing at %d: %d then %d", i, second[i-1].Seq, second[i].Seq)
		}
	}
}

func TestServer_SRV13_SlowSubscriberDropped(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "hi"})
	slow, cancelSlow := s.Subscribe(1)
	defer cancelSlow()
	fast, cancelFast := s.Subscribe(64)
	defer cancelFast()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	collect(t, fast) // drive the run to completion
	sawError := false
	for ev := range slow {
		if ev.Type == proto.EventError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("a full subscriber should be dropped with an error")
	}
}

func TestServer_SRV14_MessagesPersisted(t *testing.T) {
	s, _ := testServer(t,
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "read", Args: `{"path":"f.txt"}`}}},
		testkit.Turn{Text: "done"},
	)
	if err := os.WriteFile(filepath.Join(s.cwd, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events, cancel := s.Subscribe(256)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "read it"})
	collect(t, events)
	roles := []string{}
	for _, e := range s.store.Entries() {
		if e.Type == session.TypeMessage && e.Message != nil {
			roles = append(roles, string(e.Message.Role))
		}
	}
	if len(roles) < 3 || roles[0] != "user" {
		t.Fatalf("persisted roles = %v", roles)
	}
}

func TestServer_SRV16_NoSession(t *testing.T) {
	cfg := config.Defaults()
	cfg.Tools.Active = []string{"read"}
	s, err := New(Options{
		Config:    &cfg,
		CWD:       t.TempDir(),
		Home:      t.TempDir(),
		NoSession: true,
		Provider:  testkit.NewProvider(testkit.RealClock()),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if s.store != nil || s.path != "" {
		t.Fatalf("NoSession should not open a store: store=%v path=%q", s.store, s.path)
	}
}

func TestServer_SRV21_StateChanges(t *testing.T) {
	s, _ := testServer(t)
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdSetModel, Model: "openai/gpt-6-luna"})
	s.Handle(context.Background(), proto.Command{Type: proto.CmdSetThinking, Level: "high"})
	s.Handle(context.Background(), proto.Command{Type: proto.CmdSetContextLimit, Tokens: intPtr(4096)})
	changes := 0
	for {
		select {
		case ev := <-events:
			if ev.Type == proto.EventStateChanged {
				changes++
			}
		default:
			if changes != 3 {
				t.Fatalf("state_changed events = %d, want 3", changes)
			}
			found := false
			for _, e := range s.store.Entries() {
				if e.Type == session.TypeModelChange {
					found = true
				}
			}
			if !found {
				t.Fatal("set_model should append a model_change entry")
			}
			return
		}
	}
}

// intPtr returns a pointer to v.
func intPtr(v int) *int { return &v }
