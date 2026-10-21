package ext

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

func TestCheck_CHK01_Healthy(t *testing.T) {
	dir := echoDir(t, "echo", "")
	if problems := Check(context.Background(), dir, CheckOptions{}); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestCheck_CHK02_SeveralProblems(t *testing.T) {
	t.Setenv("ECHO_TOOL_NAMES", "read,bad")
	t.Setenv("ECHO_TOOL_PARAMS", `{"type":"array"}`)
	dir := echoDir(t, "echo", "")
	problems := Check(context.Background(), dir, CheckOptions{})
	if len(problems) < 2 {
		t.Fatalf("problems = %v, want at least a conflict and a bad schema", problems)
	}
}

func TestCheck_CHK03_NeverRegisters(t *testing.T) {
	t.Setenv("ECHO_NO_REGISTER", "1")
	clk := testkit.NewFakeClock(time.Unix(0, 0))
	dir := echoDir(t, "echo", "")
	done := make(chan []error, 1)
	go func() { done <- Check(context.Background(), dir, CheckOptions{Clock: clk}) }()
	clk.BlockUntil(t, 1)
	clk.Advance(11 * time.Second)
	select {
	case problems := <-done:
		if len(problems) == 0 {
			t.Fatal("expected a problem for an extension that never registers")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Check hung")
	}
}

func TestCheck_CHK04_BuildFailure(t *testing.T) {
	dir := rawEchoDir(t, "echo", "build: [\"sh\", \"-c\", \"exit 1\"]\n")
	problems := Check(context.Background(), dir, CheckOptions{})
	if len(problems) == 0 {
		t.Fatal("expected a build failure")
	}
}

// A build that outlives its timeout is killed with its children, which hold the
// output pipe open; killing only the shell would leave Check waiting for them.
func TestCheck_CHK04b_BuildTimeoutKillsTheTree(t *testing.T) {
	dir := rawEchoDir(t, "echo", "build: [\"sh\", \"-c\", \"sleep 30; true\"]\n")
	done := make(chan []error, 1)
	go func() { done <- Check(context.Background(), dir, CheckOptions{Timeout: 100 * time.Millisecond}) }()
	select {
	case problems := <-done:
		if len(problems) == 0 {
			t.Fatal("a build that timed out was not reported")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Check waited for the build's children after the timeout")
	}
}

func TestCheck_CHK05_DoesNotInstall(t *testing.T) {
	dir := t.TempDir()
	writeRepoFiles(t, dir, map[string]string{"extension.yaml": "name: hello\nversion: 1.0.0\ndescription: x\nrun: [echo]\n"})
	before, _ := os.ReadDir(dir)
	root := filepath.Join(t.TempDir(), "extensions")
	_ = Check(context.Background(), dir, CheckOptions{})
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatalf("Check changed the directory: before=%d after=%d", len(before), len(after))
	}
	if pathExists(root) {
		t.Fatal("Check created an install root")
	}
}

func TestReload_RLD01_RestartsEverything(t *testing.T) {
	reg := toolReg()
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Registry: reg})
	if errs := h.Reload(context.Background()); len(errs) != 0 {
		t.Fatalf("reload errors: %v", errs)
	}
	if stateOf(h, "echo") != stateRunning {
		t.Fatalf("state after reload = %s", stateOf(h, "echo"))
	}
	if _, ok := reg.Get("echo"); !ok {
		t.Fatal("tool missing after reload")
	}
}

func TestReload_RLD02_ChangedManifest(t *testing.T) {
	dir := t.TempDir()
	manifest := func(extra string) {
		body := "name: decl\nversion: 1.0.0\ndescription: x\ntools:\n  - name: one\n    description: x\n    parameters: {type: object}\n    exec: [echo]" + extra
		if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest("")
	reg := toolReg()
	h := startHost(t, []string{dir}, HostOptions{Registry: reg})
	if _, ok := reg.Get("one"); !ok {
		t.Fatalf("first tool missing: %v", reg.Names())
	}
	manifest("\n  - name: two\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n")
	if errs := h.Reload(context.Background()); len(errs) != 0 {
		t.Fatalf("reload errors: %v", errs)
	}
	if _, ok := reg.Get("two"); !ok {
		t.Fatalf("new tool missing after reload: %v", reg.Names())
	}
}

func TestReload_RLD03_RebuildChangedSource(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "built")
	body := "name: decl\nversion: 1.0.0\ndescription: x\nbuild: [\"sh\", \"-c\", \"touch " + marker + "\"]\ntools:\n  - name: one\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n"
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	h := startHost(t, []string{dir}, HostOptions{})
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(body+"  - name: two\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if errs := h.Reload(context.Background()); len(errs) != 0 {
		t.Fatalf("reload errors: %v", errs)
	}
	if !pathExists(marker) {
		t.Fatal("build did not run on reload")
	}
}

func TestReload_RLD04_OneBroken(t *testing.T) {
	good := echoDir(t, "good", "")
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "extension.yaml"), []byte("name: broken\nversion: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := toolReg()
	h := startHost(t, []string{good, broken}, HostOptions{Registry: reg})
	if errs := h.Reload(context.Background()); len(errs) == 0 {
		t.Fatal("expected a reload error for the broken manifest")
	}
	if stateOf(h, "good") != stateRunning {
		t.Fatalf("good extension state = %s", stateOf(h, "good"))
	}
}

func TestReload_RLD05_NoStaleTools(t *testing.T) {
	dir := t.TempDir()
	write := func(tool string) {
		body := "name: decl\nversion: 1.0.0\ndescription: x\ntools:\n  - name: " + tool + "\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n"
		if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("one")
	reg := toolReg()
	h := startHost(t, []string{dir}, HostOptions{Registry: reg})
	write("two")
	if errs := h.Reload(context.Background()); len(errs) != 0 {
		t.Fatalf("reload errors: %v", errs)
	}
	if _, ok := reg.Get("one"); ok {
		t.Fatalf("stale tool one still present: %v", reg.Names())
	}
	if _, ok := reg.Get("two"); !ok {
		t.Fatalf("new tool two missing: %v", reg.Names())
	}
}

func TestReload_RLD06_DuringRun(t *testing.T) {
	reg := toolReg()
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Registry: reg, Env: envWith("ECHO_SLOW_TOOL_MS=200")})
	done := make(chan struct{})
	go func() { _ = reg.Execute(context.Background(), "echo", nil, nil); close(done) }()
	if errs := h.Reload(context.Background()); len(errs) != 0 {
		t.Fatalf("reload errors: %v", errs)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tool call hung across a reload")
	}
}

func TestReload_RLD07_ReplayAfterReload(t *testing.T) {
	trace := t.TempDir() + "/trace"
	sess := &fakeSession{}
	if err := sess.AppendEntry("todo.state", []byte(`{"n":1}`), true); err != nil {
		t.Fatal(err)
	}
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{Session: sess, Env: envWith("ECHO_ENTRIES=todo.state", "ECHO_TRACE="+trace)})
	if errs := h.Reload(context.Background()); len(errs) != 0 {
		t.Fatalf("reload errors: %v", errs)
	}
	waitContains(t, trace, "replay", 3*time.Second)
}

// toolReg builds an empty registry.
func toolReg() *tool.Registry { return tool.NewRegistry() }

// writeRepoFiles writes files below dir, creating parents.
func writeRepoFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// failingBuild is a CommandRunner whose build step fails and counts its runs.
type failingBuild struct{ runs int }

func (f *failingBuild) Run(context.Context, string, []string) error {
	f.runs++
	return errors.New("compile error")
}

// A build that fails on reload must be reported, and must be tried again on the
// next reload rather than remembered as done.
func TestReload_RLD03b_FailedBuildIsReportedAndRetried(t *testing.T) {
	dir := t.TempDir()
	body := "name: decl\nversion: 1.0.0\ndescription: x\nbuild: [make]\ntools:\n  - name: one\n    description: x\n    parameters: {type: object}\n    exec: [echo]\n"
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	build := &failingBuild{}
	h := startHost(t, []string{dir}, HostOptions{Run: build})
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(body+"# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := h.Reload(context.Background())
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "compile error") {
		t.Fatalf("reload errors = %v, want the build failure", errs)
	}
	if errs := h.Reload(context.Background()); len(errs) != 1 {
		t.Fatalf("second reload errors = %v, want the build retried and reported again", errs)
	}
	if build.runs != 2 {
		t.Fatalf("build ran %d times, want 2", build.runs)
	}
}

// pathExists reports whether path can be stat'd.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
