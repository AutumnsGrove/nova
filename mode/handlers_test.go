package mode

import (
	"context"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/session"
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

func TestHandlers_SRV31_GetCommands(t *testing.T) {
	s, _ := testServer(t)
	if err := writeFile(s.cwd+"/.nova/prompts/foo.md", "do foo $1"); err != nil {
		t.Fatal(err)
	}
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetCommands})
	if !resp.OK || !bytesContain(resp.Data, "\"foo\"") || !bytesContain(resp.Data, "\"help\"") {
		t.Fatalf("get_commands = %+v data=%s", resp, resp.Data)
	}
}

func TestHandlers_SRV32_RunCommandHelp(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "help"})
	if !resp.OK || !bytesContain(resp.Data, "commands:") {
		t.Fatalf("run_command help = %+v data=%s", resp, resp.Data)
	}
}

func TestHandlers_SRV32_RunCommandUnknown(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "nope"})
	if resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("run_command unknown = %+v", resp)
	}
}

func TestHandlers_SRV45_RunCommandQuit(t *testing.T) {
	for _, name := range []string{"quit", "exit"} {
		s, _ := testServer(t)
		resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: name})
		if !resp.OK {
			t.Fatalf("run_command %s = %+v", name, resp)
		}
	}
}

func TestHandlers_SRV45_GetCommandsListsQuit(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetCommands})
	if !resp.OK || !bytesContain(resp.Data, "\"quit\"") || !bytesContain(resp.Data, "\"exit\"") {
		t.Fatalf("get_commands = %+v data=%s", resp, resp.Data)
	}
}

func TestHandlers_SRV19_CompactNothing(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdCompact})
	if resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("compact = %+v, want bad_request", resp)
	}
}

func TestHandlers_SRV24_NavigateAndName(t *testing.T) {
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

func TestHandlers_SRV24_SessionNew(t *testing.T) {
	s, _ := testServer(t)
	before := s.path
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionNew})
	if !resp.OK || s.path == before {
		t.Fatalf("session.new = %+v, path %q -> %q", resp, before, s.path)
	}
}

// SRV46: /new (run_command "new") starts a fresh session, the same thing
// session.new does, so the advertised command stops returning unknown.
func TestHandlers_SRV46_RunCommandNew(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "hello"})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	collect(t, events)
	if msgs := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetMessages}); !bytesContain(msgs.Data, "hello") {
		t.Fatalf("precondition: old conversation missing: %s", msgs.Data)
	}

	before := s.path
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "new"})
	if !resp.OK {
		t.Fatalf("run_command new = %+v", resp)
	}
	if s.path == before {
		t.Fatalf("/new did not open a new session: path still %q", s.path)
	}
	msgs := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetMessages})
	if !msgs.OK || bytesContain(msgs.Data, "hello") {
		t.Fatalf("conversation not cleared by /new: %s", msgs.Data)
	}
}

// CLR02: /clear is an alias for /new.
func TestHandlers_CLR02_RunCommandClear(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Text: "hello"})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "hi"})
	collect(t, events)

	before := s.path
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "clear"}); !resp.OK {
		t.Fatalf("run_command clear = %+v", resp)
	}
	if s.path == before {
		t.Fatalf("/clear did not open a new session: path still %q", s.path)
	}
}

// CLR03: both names are advertised to clients.
func TestHandlers_CLR03_GetCommandsListsNewAndClear(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetCommands})
	if !resp.OK || !bytesContain(resp.Data, "\"new\"") || !bytesContain(resp.Data, "\"clear\"") {
		t.Fatalf("get_commands = %+v data=%s", resp, resp.Data)
	}
}

// CLR04: /new is refused while a run is active, like session.new.
func TestHandlers_CLR04_RunCommandNewBusy(t *testing.T) {
	s, _ := testServer(t, testkit.Turn{Hang: true})
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	waitFor(t, events, proto.EventRunStart)
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "new"}); resp.OK || resp.Error.Code != proto.CodeBusy {
		t.Fatalf("run_command new while busy = %+v, want busy", resp)
	}
	s.agent.Abort()
}

func TestHandlers_SRV29_BashRecorded(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdBash, Command: "echo hello-bash"})
	if !resp.OK || !bytesContain(resp.Data, "hello-bash") {
		t.Fatalf("bash = %+v data=%s", resp, resp.Data)
	}
	found := false
	for _, e := range s.store.Entries() {
		if e.Type == session.TypeCustomMessage && strings.Contains(e.Content, "hello-bash") {
			found = true
		}
	}
	if !found {
		t.Fatal("bash should append a custom_message entry")
	}
	msgs, err := s.store.Messages("")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "hello-bash") {
			t.Fatal("the bash command must not enter the context")
		}
	}
}

func TestHandlers_SRV30_BashQuiet(t *testing.T) {
	s, _ := testServer(t)
	if resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdBash, Command: "!!echo quiet-bash"}); !resp.OK {
		t.Fatalf("bash = %+v", resp)
	}
	for _, e := range s.store.Entries() {
		if strings.Contains(e.Content, "quiet-bash") {
			t.Fatal("a !! command must not be recorded")
		}
	}
}

// bytesContain reports whether data holds substr.
func bytesContain(data []byte, substr string) bool {
	return strings.Contains(string(data), substr)
}
