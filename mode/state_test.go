package mode

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// noSessionServer builds a server that keeps its transcript in memory only.
func noSessionServer(t *testing.T, turns ...testkit.Turn) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Tools.Active = []string{"read"}
	cfg.Compaction.Enabled = boolPtr(false)
	cfg.Extensions.Disable = bundledNames()
	s, err := New(Options{
		Config:    &cfg,
		CWD:       t.TempDir(),
		Home:      t.TempDir(),
		NoSession: true,
		Provider:  testkit.NewProvider(testkit.RealClock(), turns...),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// waitIdle blocks until the agent has released its run. run_end is emitted a
// moment before that, and a session command sent in the gap is refused as busy.
func waitIdle(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.agent.Running() {
		if time.Now().After(deadline) {
			t.Fatal("the agent never became idle")
		}
		runtime.Gosched()
	}
}

// effectiveLimit reads get_state's effective_limit.
func effectiveLimit(t *testing.T, s *Server) int {
	t.Helper()
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetState})
	var state struct {
		EffectiveLimit int `json:"effective_limit"`
	}
	decodeData(t, resp, &state)
	return state.EffectiveLimit
}

// A model switch must move the compaction window with it: the trigger is the
// smaller of the user's limit and the selected model's own window.
func TestServer_SRV47_SetModelRefreshesWindow(t *testing.T) {
	s, _ := testServer(t)
	if got := effectiveLimit(t, s); got != 0 {
		t.Fatalf("effective_limit for an unknown model = %d, want 0", got)
	}
	const opusWindow = 1_000_000
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSetModel, Model: "anthropic/claude-opus-5.5"}); !resp.OK {
		t.Fatalf("set_model = %+v", resp)
	}
	if got := effectiveLimit(t, s); got != opusWindow {
		t.Fatalf("effective_limit after set_model = %d, want the model's window %d", got, opusWindow)
	}
}

// Every event a client sees carries a timestamp, including the ones the server
// itself raises rather than the agent.
func TestServer_SRV48_ServerEventsCarryTimestamps(t *testing.T) {
	s, _ := testServer(t)
	events, cancel := s.Subscribe(16)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdSetThinking, Level: "high"})
	ev := waitFor(t, events, proto.EventStateChanged)
	if ev.TS.IsZero() {
		t.Fatalf("state_changed has no timestamp: %+v", ev)
	}
}

// Without a session store the transcript lives in memory, and get_messages must
// still return it.
func TestServer_SRV49_GetMessagesWithoutSession(t *testing.T) {
	s := noSessionServer(t, testkit.Turn{Text: "from memory"})
	if got := promptAndWait(t, s, "hi"); !got.OK {
		t.Fatalf("prompt = %+v", got)
	}
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetMessages})
	var data struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	decodeData(t, resp, &data)
	if len(data.Messages) != 2 || data.Messages[1].Content != "from memory" {
		t.Fatalf("get_messages = %s, want the user and assistant messages", resp.Data)
	}
}

// The last response's prompt size describes the old session. After a switch it
// must not make a fresh, short conversation look over the limit.
func TestServer_SRV50_SessionSwitchForgetsLastUsage(t *testing.T) {
	switches := map[string]func(s *Server) proto.Command{
		"new": func(*Server) proto.Command { return proto.Command{Type: proto.CmdSessionNew} },
		"navigate": func(s *Server) proto.Command {
			return proto.Command{Type: proto.CmdSessionNavigate, Entry: s.store.Entries()[0].ID}
		},
	}
	for name, switchCmd := range switches {
		t.Run(name, func(t *testing.T) {
			s, _ := testServer(t,
				testkit.Turn{Text: "one", Usage: &provider.Usage{PromptTokens: 950}},
				testkit.Turn{Text: "two"},
			)
			s.limit = 1000
			s.compactor.Settings.Enabled = true
			if got := promptAndWait(t, s, "first"); !got.OK {
				t.Fatalf("prompt = %+v", got)
			}
			waitIdle(t, s)
			if resp := s.Handle(context.Background(), switchCmd(s)); !resp.OK {
				t.Fatalf("%s = %+v", name, resp)
			}
			events, cancel := s.Subscribe(256)
			defer cancel()
			if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "second"}); !resp.OK {
				t.Fatalf("prompt = %+v", resp)
			}
			for _, ev := range collect(t, events) {
				if ev.Type == proto.EventError {
					t.Fatalf("the switched conversation raised an error event: %+v", ev.Error)
				}
			}
		})
	}
}

// Session commands may arrive from several HTTP requests at once, so readers of
// the store and the compactor must not race the commands that replace them. The
// assertion is the race detector.
func TestServer_SRV51_ConcurrentSessionCommandsAreRaceFree(t *testing.T) {
	s, _ := testServer(t)
	var wg sync.WaitGroup
	run := func(n int, cmd proto.Command) {
		defer wg.Done()
		for range n {
			s.Handle(context.Background(), cmd)
		}
	}
	wg.Add(5)
	go run(10, proto.Command{Type: proto.CmdSessionNew})
	go run(30, proto.Command{Type: proto.CmdGetMessages})
	go run(30, proto.Command{Type: proto.CmdGetState})
	go run(30, proto.Command{Type: proto.CmdSessionTree})
	go run(30, proto.Command{Type: proto.CmdSetModel, Model: "openai/gpt-6-luna"})
	wg.Wait()
}

// A failed set_tools must leave the registry as it was, not half rebuilt.
func TestServer_SRV52_SetToolsUnknownKeepsRegistry(t *testing.T) {
	s, _ := testServer(t)
	before := s.tools.Names()
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSetTools, Active: []string{"bash", "nope"}})
	wantCode(t, resp, proto.CodeBadRequest)
	if after := s.tools.Names(); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("registry = %v after a rejected set_tools, want %v", after, before)
	}
}

// set_tools names built-in tools. A tool an extension registered is not one of
// them, so changing the active set must leave it where it is.
func TestServer_SRV56_SetToolsKeepsExtensionTools(t *testing.T) {
	s, _ := testServer(t)
	s.tools.MustRegister(tool.New(tool.Info{Name: "from_ext", Description: "x", Parameters: map[string]any{"type": "object"}},
		func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
			return tool.Text("ok"), nil
		}))
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSetTools, Active: []string{"bash", "glob"}}); !resp.OK {
		t.Fatalf("set_tools = %+v", resp)
	}
	if got, want := strings.Join(slices.Sorted(slices.Values(s.tools.Names())), ","), "bash,from_ext,glob"; got != want {
		t.Fatalf("registry = %q, want %q", got, want)
	}
}

// A session directory that cannot be read is an error to report, not an empty
// list; only a directory that does not exist yet means "no sessions".
func TestHandlers_SRV53_SessionListReportsReadErrors(t *testing.T) {
	s, _ := testServer(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionList}); !resp.OK || string(resp.Data) != "[]" {
		t.Fatalf("session.list with no directory = %+v data=%s, want an empty list", resp, resp.Data)
	}
	// A regular file where the directory tree should be makes ReadDir fail with
	// something other than "does not exist".
	if err := writeFile(filepath.Join(home, ".local"), "not a directory"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionList}), proto.CodeInternal)
}
