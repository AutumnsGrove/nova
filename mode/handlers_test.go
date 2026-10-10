package mode

import (
	"context"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
)

func TestHandlers_GetMessages(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "hello"})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	collect(t, events)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetMessages})
	if !resp.OK || !bytesContain(resp.Data, "hello") {
		t.Fatalf("get_messages = %+v data=%s", resp, resp.Data)
	}
}

func TestHandlers_ListModels(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdListModels})
	if !resp.OK || !bytesContain(resp.Data, "deepseek/deepseek-v4.1") {
		t.Fatalf("list_models = %+v data=%s", resp, resp.Data)
	}
}

func TestHandlers_GetCommands_IncludesTemplate(t *testing.T) {
	s, _ := testServer(t)
	if err := writeFile(s.cwd+"/.nova/prompts/foo.md", "do foo $1"); err != nil {
		t.Fatal(err)
	}
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetCommands})
	if !resp.OK || !bytesContain(resp.Data, "\"foo\"") || !bytesContain(resp.Data, "\"help\"") {
		t.Fatalf("get_commands = %+v data=%s", resp, resp.Data)
	}
}

func TestHandlers_RunCommandHelp(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "help"})
	if !resp.OK || !bytesContain(resp.Data, "commands:") {
		t.Fatalf("run_command help = %+v data=%s", resp, resp.Data)
	}
}

func TestHandlers_RunCommandUnknown(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "nope"})
	if resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("run_command unknown = %+v", resp)
	}
}

func TestHandlers_CompactNothing(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdCompact})
	if resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("compact = %+v, want bad_request", resp)
	}
}

func TestHandlers_SessionNavigateAndName(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "hi"})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	collect(t, events)

	entries := s.store.Entries()
	if len(entries) == 0 {
		t.Fatal("no session entries")
	}
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionNavigate, Entry: entries[0].ID})
	if !resp.OK {
		t.Fatalf("navigate = %+v", resp)
	}
	if s.store.Leaf() != entries[0].ID {
		t.Fatalf("leaf = %q, want %q", s.store.Leaf(), entries[0].ID)
	}
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionName, Name: "my session"}); !resp.OK {
		t.Fatalf("session.name = %+v", resp)
	}
}

// bytesContain reports whether data holds substr.
func bytesContain(data []byte, substr string) bool {
	return strings.Contains(string(data), substr)
}
