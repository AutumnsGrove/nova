package ext

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// echoBin is the test extension built once for the package.
var echoBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nova-echoext")
	if err != nil {
		panic(err)
	}
	echoBin = filepath.Join(dir, "echoext")
	out, err := exec.Command("go", "build", "-o", echoBin, "./testdata/echoext").CombinedOutput()
	if err != nil {
		panic("build echoext: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// echoDir writes a manifest that runs echoext with the given extra YAML.
func echoDir(t *testing.T, name, extra string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "name: " + name + "\nversion: 1.0.0\ndescription: test extension\nrun: [\"" + echoBin + "\"]\n" +
		"capabilities: [tools, commands, session:append_entry, session:read, model:complete, providers,\n" +
		"  ui:notify, ui:confirm, ui:select, ui:input, ui:form, ui:status, ui:widget,\n" +
		"  hooks:tool_call, hooks:tool_result, hooks:transform_context, hooks:before_provider_request,\n" +
		"  hooks:message_end, hooks:turn_end, hooks:agent_end]\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeSession records custom entries.
type fakeSession struct {
	mu      sync.Mutex
	entries []fakeEntry
}

type fakeEntry struct {
	kind string
	data json.RawMessage
	keep bool
}

func (s *fakeSession) AppendEntry(kind string, data json.RawMessage, keep bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, fakeEntry{kind, append(json.RawMessage(nil), data...), keep})
	return nil
}

func (s *fakeSession) Entries(kind string) []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []json.RawMessage
	for _, e := range s.entries {
		if e.kind == kind {
			out = append(out, e.data)
		}
	}
	return out
}

// fakeUI records UI traffic.
type fakeUI struct {
	mu       sync.Mutex
	requests []proto.UIRequest
	notices  []proto.UIRequest
	answer   proto.UIResponse
	err      error
}

func (u *fakeUI) Request(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests = append(u.requests, req)
	if u.err != nil {
		return proto.UIResponse{}, u.err
	}
	resp := u.answer
	resp.Type = proto.FrameUIResponse
	resp.ID = req.ID
	return resp, nil
}

func (u *fakeUI) Notify(req proto.UIRequest) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.notices = append(u.notices, req)
}

// logCapture collects host log lines.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) log(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, level+" "+msg)
}

func (l *logCapture) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// startHost builds and starts a host for dirs, with cleanup.
func startHost(t *testing.T, dirs []string, opt HostOptions) *Host {
	t.Helper()
	opt.Dirs = dirs
	if opt.Clock == nil {
		opt.Clock = testkit.NewFakeClock(time.Unix(0, 0))
	}
	if opt.Env == nil {
		opt.Env = os.Environ
	}
	logs := &logCapture{}
	if opt.Log == nil {
		opt.Log = logs.log
	}
	h := NewHost(opt)
	errs := h.Start(context.Background())
	for _, err := range errs {
		t.Logf("start error: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("host logs:\n%s", logs.all())
		}
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stop := make(chan struct{})
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
					if clk, ok := opt.Clock.(*testkit.FakeClock); ok {
						clk.Advance(time.Second)
					}
					time.Sleep(time.Millisecond)
				}
			}
		}()
		_ = h.Shutdown(ctx)
		close(stop)
	})
	return h
}

// envWith returns an Env function that adds kv to the process environment.
func envWith(kv ...string) func() []string {
	return func() []string { return append(os.Environ(), kv...) }
}

// rawEchoDir writes a manifest verbatim, so a test can omit capabilities.
func rawEchoDir(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "name: " + name + "\nversion: 1.0.0\ndescription: test extension\nrun: [\"" + echoBin + "\"]\n" + body
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHost_SUP01_StartRegister(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Registry: reg})
	if _, ok := reg.Get("echo"); !ok {
		t.Fatalf("echo tool not registered: %v status=%+v", reg.Names(), h.Status())
	}
	status := h.Status()
	if len(status) != 1 || status[0].State != stateRunning {
		t.Fatalf("status = %+v", status)
	}
}

func TestHost_HST01_ToolRoundTrip(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Registry: reg})
	_, ok := reg.Get("echo")
	if !ok {
		t.Fatal("echo tool missing")
	}
	res := reg.Execute(context.Background(), "echo", json.RawMessage(`{"x":1}`), &tool.Context{WorkDir: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.Content, `{"x":1}`) {
		t.Fatalf("content = %q", res.Content)
	}
	_ = h
}

func TestHost_HST25_ToolResultParts(t *testing.T) {
	reg := tool.NewRegistry()
	dir := echoDir(t, "echo", "")
	_ = startHost(t, []string{dir}, HostOptions{Registry: reg, Env: envWith("ECHO_IMAGE=1")})
	res := reg.Execute(context.Background(), "echo", json.RawMessage(`{}`), &tool.Context{WorkDir: t.TempDir()})
	if len(res.Parts) != 1 || res.Parts[0].Type != message.PartImage || res.Parts[0].Data != "aGk=" {
		t.Fatalf("parts = %+v, want one image part", res.Parts)
	}
}
