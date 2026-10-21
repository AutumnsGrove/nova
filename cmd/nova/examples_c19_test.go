package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// exampleNames are the eleven examples the spec requires.
var exampleNames = []string{
	"hello", "word-count", "block-rm", "hello-go", "path-guard",
	"status-segment", "clock-widget", "ask-demo", "session-notes", "summarize",
	"web-search",
}

// exampleDir returns the absolute path of one example.
func exampleDir(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "examples", "extensions", name))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestExamples_EXM01_ElevenExist(t *testing.T) {
	for _, name := range exampleNames {
		dir := exampleDir(t, name)
		if _, err := os.Stat(filepath.Join(dir, "extension.yaml")); err != nil {
			t.Fatalf("example %s has no manifest: %v", name, err)
		}
	}
}

func TestExamples_EXM02_Under100Lines(t *testing.T) {
	for _, name := range exampleNames {
		entries, err := os.ReadDir(exampleDir(t, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(exampleDir(t, name), entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if lines := strings.Count(string(data), "\n"); lines >= 100 {
				t.Fatalf("%s/%s is %d lines, want under 100", name, entry.Name(), lines)
			}
		}
	}
}

func TestExamples_EXM03_Readme(t *testing.T) {
	for _, name := range exampleNames {
		if _, err := os.Stat(filepath.Join(exampleDir(t, name), "README.md")); err != nil {
			t.Fatalf("example %s has no README: %v", name, err)
		}
	}
}

func TestExamples_EXM04_ExtCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("ext.Check builds the Go examples")
	}
	for _, name := range exampleNames {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		problems := ext.Check(ctx, exampleDir(t, name), ext.CheckOptions{Timeout: 120 * time.Second})
		cancel()
		if len(problems) > 0 {
			t.Fatalf("example %s failed ext.Check: %v", name, problems)
		}
	}
}

// fakeSession is an in-memory ext.SessionAPI.
type fakeSession struct {
	mu      sync.Mutex
	entries map[string][]json.RawMessage
}

func (s *fakeSession) AppendEntry(kind string, data json.RawMessage, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[string][]json.RawMessage{}
	}
	s.entries[kind] = append(s.entries[kind], data)
	return nil
}

func (s *fakeSession) Entries(kind string) []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]json.RawMessage(nil), s.entries[kind]...)
}

// fakeUI records UI requests and answers select and input.
type fakeUI struct {
	mu        sync.Mutex
	requests  []proto.UIRequest
	selectIDs []string
	inputText string
}

func (u *fakeUI) Notify(req proto.UIRequest) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests = append(u.requests, req)
}

func (u *fakeUI) Request(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	u.Notify(req)
	switch req.Method {
	case "select":
		return proto.UIResponse{OK: true, IDs: u.selectIDs}, nil
	case "input":
		return proto.UIResponse{OK: true, Text: u.inputText}, nil
	}
	return proto.UIResponse{OK: true}, nil
}

func (u *fakeUI) methods() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]string, 0, len(u.requests))
	for _, r := range u.requests {
		out = append(out, r.Method)
	}
	return out
}

// startExampleHost starts a host over the given example directories.
func startExampleHost(t *testing.T, names []string, opt ext.HostOptions) *ext.Host {
	t.Helper()
	dirs := make([]string, len(names))
	for i, name := range names {
		dirs[i] = exampleDir(t, name)
	}
	opt.Dirs = dirs
	if opt.Registry == nil {
		opt.Registry = tool.NewRegistry()
	}
	h := ext.NewHost(opt)
	if errs := h.Start(context.Background()); len(errs) > 0 {
		t.Fatalf("start %v: %v", names, errs)
	}
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })
	return h
}

func TestExamples_EXM05_DeclarativeTools(t *testing.T) {
	if testing.Short() {
		t.Skip("the examples build")
	}
	h := startExampleHost(t, []string{"hello", "word-count"}, ext.HostOptions{})
	ctx := context.Background()

	hello := h.Registry().Execute(ctx, "hello", json.RawMessage(`{"name":"Ada"}`), &tool.Context{WorkDir: t.TempDir()})
	if hello.IsError || !strings.Contains(hello.Content, "Hello, Ada!") {
		t.Fatalf("hello = %+v, want a greeting", hello)
	}

	file := filepath.Join(t.TempDir(), "words.txt")
	if err := os.WriteFile(file, []byte("one two three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	count := h.Registry().Execute(ctx, "word_count", json.RawMessage(`{"path":`+quote(file)+`}`), &tool.Context{WorkDir: t.TempDir()})
	if count.IsError || !strings.Contains(count.Content, "3") {
		t.Fatalf("word_count = %+v, want 3", count)
	}
}

func TestExamples_EXM06_BlockRm(t *testing.T) {
	h := startExampleHost(t, []string{"block-rm"}, ext.HostOptions{})
	ctx := context.Background()
	blocked := h.Hooks().ToolCall(ctx, agent.ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"rm -rf /"}`)})
	if blocked == nil || !blocked.Block {
		t.Fatalf("rm -rf was not blocked: %+v", blocked)
	}
	safe := h.Hooks().ToolCall(ctx, agent.ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"ls"}`)})
	if safe != nil && safe.Block {
		t.Fatalf("a safe command was blocked: %+v", safe)
	}
}

func TestExamples_EXM07_PathGuard(t *testing.T) {
	h := startExampleHost(t, []string{"path-guard"}, ext.HostOptions{})
	ctx := context.Background()
	blocked := h.Hooks().ToolCall(ctx, agent.ToolCall{Name: "write", Arguments: json.RawMessage(`{"path":"/etc/passwd"}`)})
	if blocked == nil || !blocked.Block {
		t.Fatalf("an outside write was not blocked: %+v", blocked)
	}
	safe := h.Hooks().ToolCall(ctx, agent.ToolCall{Name: "write", Arguments: json.RawMessage(`{"path":"src/a.go"}`)})
	if safe != nil && safe.Block {
		t.Fatalf("a project write was blocked: %+v", safe)
	}
}

func TestExamples_EXM08_StatusAndWidget(t *testing.T) {
	ui := &fakeUI{}
	h := startExampleHost(t, []string{"status-segment", "clock-widget"}, ext.HostOptions{UI: ui})
	h.Hooks().TurnEnd(context.Background(), agent.Turn{Index: 1})
	methods := strings.Join(ui.methods(), ",")
	if !strings.Contains(methods, "status") || !strings.Contains(methods, "widget") {
		t.Fatalf("UI methods = %v, want status and widget", methods)
	}
}

func TestExamples_EXM09_AskDemo(t *testing.T) {
	ui := &fakeUI{selectIDs: []string{"bugs"}, inputText: "hello"}
	h := startExampleHost(t, []string{"ask-demo"}, ext.HostOptions{UI: ui})
	out, err := h.RunCommand(context.Background(), "ask", "")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if !strings.Contains(out, "topic=bugs") || !strings.Contains(out, "note=hello") {
		t.Fatalf("ask output = %q", out)
	}
}

func TestExamples_EXM10_SessionNotesReplay(t *testing.T) {
	sess := &fakeSession{}
	ctx := context.Background()
	kinds := map[string][]string{"session-notes": {"session-notes.note"}}
	first := startExampleHost(t, []string{"session-notes"}, ext.HostOptions{Session: sess, EntryKinds: kinds})
	if _, err := first.RunCommand(ctx, "note", "buy milk"); err != nil {
		t.Fatalf("note: %v", err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	second := startExampleHost(t, []string{"session-notes"}, ext.HostOptions{Session: sess, EntryKinds: kinds})
	out, err := second.RunCommand(ctx, "notes", "")
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if !strings.Contains(out, "buy milk") {
		t.Fatalf("notes did not replay: %q", out)
	}
}

func TestExamples_EXM11_Summarize(t *testing.T) {
	calls := 0
	complete := func(_ context.Context, _ provider.Request) (*provider.Response, error) {
		calls++
		return &provider.Response{Message: message.Message{Role: message.RoleAssistant, Content: "short summary"}}, nil
	}
	h := startExampleHost(t, []string{"summarize"}, ext.HostOptions{Complete: complete})
	out, err := h.RunCommand(context.Background(), "summarize", "a very long text")
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Complete called %d times, want 1", calls)
	}
	if !strings.Contains(out, "short summary") {
		t.Fatalf("summarize output = %q", out)
	}
}

func TestExamples_EXM12_HelloGoBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("hello-go builds with the SDK")
	}
	h := startExampleHost(t, []string{"hello-go"}, ext.HostOptions{})
	res := h.Registry().Execute(context.Background(), "hello", json.RawMessage(`{"name":"Ada"}`), &tool.Context{WorkDir: t.TempDir()})
	if res.IsError || !strings.Contains(res.Content, "Hello, Ada!") {
		t.Fatalf("hello-go = %+v, want a greeting", res)
	}
}

// quote renders a string as a JSON string literal.
func quote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

// TestExamples_EXM13_WebSearchAdapter checks the provider-agnostic search
// example runs as installed: the canned backend answers, limit is honoured, and
// an empty query is a result the model can read.
func TestExamples_EXM13_WebSearchAdapter(t *testing.T) {
	h := startExampleHost(t, []string{"web-search"}, ext.HostOptions{})
	ctx := context.Background()

	got := h.Registry().Execute(ctx, "web_search", json.RawMessage(`{"query":"nova caching","limit":1}`), &tool.Context{})
	if got.IsError || !strings.Contains(got.Content, "Example result for nova caching") {
		t.Fatalf("web_search = %+v, want the canned hit", got)
	}
	if strings.Contains(got.Content, "Second example result") {
		t.Fatalf("limit 1 was ignored: %q", got.Content)
	}

	empty := h.Registry().Execute(ctx, "web_search", json.RawMessage(`{"query":"  "}`), &tool.Context{})
	if !strings.Contains(empty.Content, "query is required") {
		t.Fatalf("empty query = %+v, want a readable error", empty)
	}
}
