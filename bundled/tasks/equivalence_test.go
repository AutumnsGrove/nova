package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

var (
	buildOnce  sync.Once
	exampleBin string
	buildErr   error
)

// buildExample builds the subprocess port once for the package.
func buildExample(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "todo-like-sdk")
		if err != nil {
			buildErr = err
			return
		}
		exampleBin = filepath.Join(dir, "todo-like-sdk")
		cmd := exec.Command("go", "build", "-o", exampleBin, "./examples/extensions/todo-like-sdk")
		cmd.Dir = filepath.Join("..", "..")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("build port: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return exampleBin
}

// exampleDir writes a manifest that runs the built port.
func exampleDir(t *testing.T, bin string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "name: todo-like-sdk\nversion: 0.1.0\ndescription: port of tasks\n" +
		"run: [\"" + bin + "\"]\ncapabilities: [tools, commands, session:append_entry]\n"
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestTasks_TSK29_EquivalenceWithSubprocess runs the same call script against
// the bundled extension and the subprocess port and compares results and the
// appended snapshots.
func TestTasks_TSK29_EquivalenceWithSubprocess(t *testing.T) {
	bin := buildExample(t)

	bundled := New()
	bsess := &fakeSession{}
	_ = bundled.Init(&ext.Env{
		Session: bsess,
		Tool:    func(tool.Tool) error { return nil },
		Command: func(ext.InProcCommand) error { return nil },
		Events:  func(func(proto.Event)) {},
	})

	psess := &fakeSession{}
	host := ext.NewHost(ext.HostOptions{
		Dirs:     []string{exampleDir(t, bin)},
		Registry: tool.NewRegistry(),
		Session:  psess,
		Emit:     func(proto.Event) {},
		Clock:    testkit.NewFakeClock(time.Unix(0, 0)),
	})
	if errs := host.Start(context.Background()); len(errs) != 0 {
		t.Fatalf("start port: %v", errs)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = host.Shutdown(ctx)
	})

	script := []string{
		`{"action":"create","title":"Fix the retry"}`,
		`{"action":"create","tasks":[{"title":"one"},{"title":"two","detail":"note"}]}`,
		`{"action":"update","id":"t1","status":"doing"}`,
		`{"action":"update","id":"t2","status":"done"}`,
		`{"action":"list"}`,
		`{"action":"delete","id":"t3"}`,
		`{"action":"update","id":"t9","status":"done"}`,
		`{"action":"clear"}`,
		`{"action":"frobnicate"}`,
	}
	for i, args := range script {
		bRes, _ := bundled.run(context.Background(), json.RawMessage(args), nil)
		sRes := host.Registry().Execute(context.Background(), "todo", json.RawMessage(args), nil)
		if bRes.Content != sRes.Content || bRes.IsError != sRes.IsError {
			t.Fatalf("step %d (%s): bundled %q isErr=%v, port %q isErr=%v", i, args, bRes.Content, bRes.IsError, sRes.Content, sRes.IsError)
		}
	}

	if err := waitEntries(psess, countEntries(bsess)); err != nil {
		t.Fatalf("port snapshots: %v", err)
	}
	bState := decodeSnapshots(t, bsess.keptEntries(stateKind))
	sState := decodeSnapshots(t, psess.keptEntries(stateKind))
	if len(bState) == 0 {
		t.Fatal("no snapshots were written")
	}
	if len(bState) != len(sState) {
		t.Fatalf("snapshot count = %d, want %d", len(sState), len(bState))
	}
	for i := range bState {
		if fmt.Sprint(bState[i].Tasks) != fmt.Sprint(sState[i].Tasks) || bState[i].Next != sState[i].Next {
			t.Fatalf("snapshot %d differs:\n bundled %+v\n port    %+v", i, bState[i], sState[i])
		}
	}
}

// countEntries counts the state snapshots in a fake session.
func countEntries(s *fakeSession) int { return len(s.keptEntries(stateKind)) }

// waitEntries waits until the session has at least n state snapshots.
func waitEntries(s *fakeSession, n int) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if countEntries(s) >= n {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("got %d entries, want %d", countEntries(s), n)
}

// decodeSnapshots decodes a list of raw state entries.
func decodeSnapshots(t *testing.T, raw []json.RawMessage) []State {
	t.Helper()
	out := make([]State, 0, len(raw))
	for _, r := range raw {
		s, ok := Decode(r)
		if !ok {
			t.Fatalf("undecodable snapshot: %s", r)
		}
		out = append(out, s)
	}
	return out
}

// keptEntries returns the raw entries of one kind.
func (s *fakeSession) keptEntries(kind string) []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []json.RawMessage
	for _, e := range s.kept {
		if e.kind == kind {
			out = append(out, e.data)
		}
	}
	return out
}
