package mode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/config"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/testkit"
)

var (
	mcpBinOnce sync.Once
	mcpBinPath string
	mcpBinErr  error
)

// mcpserverBin builds the scripted MCP server once for the package.
func mcpserverBin(t *testing.T) string {
	t.Helper()
	mcpBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-mcpserver-mode")
		if err != nil {
			mcpBinErr = err
			return
		}
		mcpBinPath = filepath.Join(dir, "mcpserver")
		out, err := exec.Command("go", "build", "-o", mcpBinPath, "../mcp/testdata/mcpserver").CombinedOutput()
		if err != nil {
			mcpBinErr = fmt.Errorf("build mcpserver: %v\n%s", err, out)
		}
	})
	if mcpBinErr != nil {
		t.Fatal(mcpBinErr)
	}
	return mcpBinPath
}

// mcpServer builds a server with one configured MCP server in a scripted role.
func mcpServer(t *testing.T, role string, mutate func(*config.Config), turns ...testkit.Turn) *Server {
	t.Helper()
	cwd, home := t.TempDir(), t.TempDir()
	prov := testkit.NewProvider(testkit.RealClock(), turns...)
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Tools.Active = []string{"read"}
	off := false
	cfg.Compaction.Enabled = &off
	cfg.Extensions.Disable = bundledNames()
	cfg.MCP.Servers = []config.MCPServer{{
		Name: "srv", Command: mcpserverBin(t),
		Args: []string{"-role", role}, TimeoutMS: 10000,
	}}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(Options{
		Config: &cfg, CWD: cwd, Home: home, Provider: prov,
		Session: filepath.Join(t.TempDir(), "session.jsonl"), Version: "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	<-s.mcpStarted
	return s
}

// toolResults returns the content of every tool message in the session.
func toolResults(s *Server) []string {
	var out []string
	for _, e := range s.store.Entries() {
		if e.Type == session.TypeMessage && e.Message != nil && string(e.Message.Role) == "tool" {
			out = append(out, e.Message.Content)
		}
	}
	return out
}

func TestMCP_MCP02_ToolsReachTheModelAndRun(t *testing.T) {
	s := mcpServer(t, "ok", nil,
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "srv__echo", Args: `{"text":"hi"}`}}},
		testkit.Turn{Text: "done"},
	)
	var offered bool
	for _, def := range s.tools.Defs() {
		if def.Function.Name == "srv__echo" {
			offered = true
			if !strings.HasPrefix(def.Function.Description, "[srv] ") {
				t.Fatalf("description = %q, want the server prefix", def.Function.Description)
			}
		}
	}
	if !offered {
		t.Fatalf("offered tools = %v, want srv__echo", s.tools.Names())
	}

	events, cancel := s.Subscribe(256)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"})
	collect(t, events)
	if got := toolResults(s); len(got) != 1 || got[0] != "echo: hi" {
		t.Fatalf("tool results = %q, want the server's answer", got)
	}
}

func TestMCP_MCP12_PermissionDenyBlocksTheCall(t *testing.T) {
	s := mcpServer(t, "ok", func(cfg *config.Config) {
		cfg.Permissions.Rules = []permission.Rule{{Tool: "srv__echo", Action: permission.Deny}}
	},
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "srv__echo", Args: `{"text":"hi"}`}}},
		testkit.Turn{Text: "done"},
	)
	events, cancel := s.Subscribe(256)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"})
	seen := collect(t, events)

	var decision string
	for _, ev := range seen {
		if ev.Type == proto.EventPermission && ev.CallID == "c1" {
			decision = ev.Decision
		}
	}
	if decision != string(permission.Deny) {
		t.Fatalf("permission event = %q, want deny", decision)
	}
	results := toolResults(s)
	if len(results) != 1 {
		t.Fatalf("tool results = %q, want one refusal", results)
	}
	if !strings.Contains(results[0], "blocked by policy") {
		t.Fatalf("tool result = %q, want a refusal the model can read", results[0])
	}
	// The server never ran the tool: its answer would have been "echo: hi".
	if strings.Contains(results[0], "echo: hi") {
		t.Fatalf("tool result = %q, want the call blocked before the server", results[0])
	}
}

func TestMCP_ServerWithoutToolsContributesNothing(t *testing.T) {
	s := mcpServer(t, "exit", nil)
	status := s.mcpStatus()
	if len(status) != 1 || status[0].State != "failed" || status[0].Error == "" {
		t.Fatalf("status = %+v, want a failed server with a reason", status)
	}
	// get_extensions reports it rather than hiding it.
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetExtensions})
	if !resp.OK {
		t.Fatalf("get_extensions = %+v", resp)
	}
	var found bool
	for _, row := range decodeArray(t, resp) {
		if row["kind"] == "mcp" && row["name"] == "srv" && row["state"] == "failed" {
			if msg, _ := row["last_error"].(string); msg == "" {
				t.Fatalf("row = %v, want the failure reason", row)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("rows = %s, want the MCP server reported", resp.Data)
	}
}

func TestMCP_ReloadKeepsTheToolsRegistered(t *testing.T) {
	s := mcpServer(t, "ok", nil)
	if _, err := s.reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	<-s.mcpStarted
	if _, ok := s.tools.Get("srv__echo"); !ok {
		t.Fatalf("registered = %v, want the tools back after a reload", s.tools.Names())
	}
	if status := s.mcpStatus(); len(status) != 1 || status[0].Tools != 4 {
		t.Fatalf("status = %+v, want one running server with four tools", status)
	}
}

func TestMCP_CommandReportsTheServers(t *testing.T) {
	s := mcpServer(t, "ok", nil)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "mcp"})
	if !resp.OK {
		t.Fatalf("run_command = %+v", resp)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		t.Fatalf("decode: %v (data=%s)", err, resp.Data)
	}
	if !strings.Contains(out.Text, "srv: running, 4 tool(s)") {
		t.Fatalf("/mcp = %q, want the server, its state and its tool count", out.Text)
	}
}

func TestMCP_CommandWithNoServers(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdRunCommand, Name: "mcp"})
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(out.Text, "no MCP servers configured") {
		t.Fatalf("/mcp = %q, want the empty answer", out.Text)
	}
}

func TestMCP_OneWarningPerDeadServer(t *testing.T) {
	// A connect failure is reported once: the host returns it and the mode start
	// reports that, so a Warn callback here would double every dead server.
	s, _ := newServerWithMCP(t, "exit", nil)
	events := drainExtensionErrors(s)
	if len(events) != 1 {
		t.Fatalf("extension_error events = %d (%v), want one per dead server", len(events), events)
	}
}

func TestMCP_ServersDoNotInheritCredentials(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-mode-secret")
	s := mcpServer(t, "ok", nil,
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "srv__env", Args: `{"name":"OPENROUTER_API_KEY"}`}}},
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c2", Name: "srv__env", Args: `{"name":"PATH"}`}}},
		testkit.Turn{Text: "done"},
	)
	events, cancel := s.Subscribe(256)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "go"})
	collect(t, events)
	results := toolResults(s)
	if len(results) != 2 {
		t.Fatalf("tool results = %q, want two", results)
	}
	if strings.Contains(results[0], "sk-mode-secret") {
		t.Fatalf("server saw the credential: %q", results[0])
	}
	// The loop records an empty tool result as a placeholder, which is what a
	// scrubbed variable looks like from the server's side.
	if results[0] != "(empty result)" {
		t.Fatalf("server saw %q for the credential, want an empty value", results[0])
	}
	if results[1] == "" {
		t.Fatal("server saw no PATH, want the rest of the environment")
	}
}

// newServerWithMCP builds a server without waiting for the MCP start, so a test
// can watch what the start reports.
func newServerWithMCP(t *testing.T, role string, mutate func(*config.Config)) (*Server, *testkit.Provider) {
	t.Helper()
	cwd, home := t.TempDir(), t.TempDir()
	prov := testkit.NewProvider(testkit.RealClock())
	cfg := config.Defaults()
	cfg.Model = "test/model"
	cfg.Tools.Active = []string{"read"}
	off := false
	cfg.Compaction.Enabled = &off
	cfg.Extensions.Disable = bundledNames()
	cfg.MCP.Servers = []config.MCPServer{{Name: "srv", Command: mcpserverBin(t), Args: []string{"-role", role}, TimeoutMS: 10000}}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(Options{Config: &cfg, CWD: cwd, Home: home, Provider: prov, NoSession: true, Version: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, prov
}

// drainExtensionErrors waits for the MCP start and returns the messages it
// reported as extension_error.
func drainExtensionErrors(s *Server) []string {
	events, cancel := s.Subscribe(64)
	defer cancel()
	<-s.mcpStarted
	var out []string
	for {
		select {
		case ev := <-events:
			if ev.Type == proto.EventExtensionError && ev.Error != nil {
				out = append(out, ev.Error.Message)
			}
			continue
		default:
			return out
		}
	}
}
