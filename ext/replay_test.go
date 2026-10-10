package ext

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHost_HST14_ReplayOnInit(t *testing.T) {
	trace := t.TempDir() + "/trace"
	sess := &fakeSession{}
	_ = sess.AppendEntry("todo.state", []byte(`{"n":1}`), true)
	_ = sess.AppendEntry("todo.state", []byte(`{"n":2}`), true)
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{
		Session:    sess,
		EntryKinds: map[string][]string{"echo": {"todo.state"}},
		Env:        envWith("ECHO_ENTRIES=todo.state", "ECHO_TRACE="+trace),
	})
	_ = h
	waitContains(t, trace, "replay:todo.state", 3*time.Second)
}

func TestHost_HST15_ReplayAfterReload(t *testing.T) {
	trace := t.TempDir() + "/trace"
	sess := &fakeSession{}
	_ = sess.AppendEntry("todo.state", []byte(`{"n":1}`), true)
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{
		Session: sess,
		Env:     envWith("ECHO_ENTRIES=todo.state", "ECHO_TRACE="+trace),
	})
	if errs := h.Reload(context.Background()); len(errs) != 0 {
		t.Fatalf("reload: %v", errs)
	}
	waitContains(t, trace, "replay:todo.state", 3*time.Second)
}

func TestHost_HST16_ReplayDeclaredKindsOnly(t *testing.T) {
	trace := t.TempDir() + "/trace"
	sess := &fakeSession{}
	_ = sess.AppendEntry("todo.state", []byte(`{"n":1}`), true)
	_ = sess.AppendEntry("other.state", []byte(`{"n":2}`), true)
	dir := echoDir(t, "echo", "")
	h := startHost(t, []string{dir}, HostOptions{
		Session:    sess,
		EntryKinds: map[string][]string{"echo": {"todo.state"}},
		Env:        envWith("ECHO_ENTRIES=todo.state", "ECHO_TRACE="+trace),
	})
	_ = h
	content := waitContains(t, trace, "replay:todo.state", 3*time.Second)
	if strings.Contains(content, "replay:other.state") {
		t.Fatalf("replayed an undeclared kind:\n%s", content)
	}
}
