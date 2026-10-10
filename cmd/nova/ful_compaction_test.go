package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/session"
	"github.com/AutumnsGrove/nova/testkit"
)

// TestCLI_FUL03_CompactionInRun drives a real compaction through the CLI: a
// small limit and keep_recent make BeforeTurn summarise the seeded history, and
// the next request carries the summary.
func TestCLI_FUL03_CompactionInRun(t *testing.T) {
	srv := testkit.NewServer(t,
		testkit.ServerTurn{Turn: testkit.Turn{Text: "prior summary"}},
		testkit.ServerTurn{Turn: testkit.Turn{Text: "final"}},
	)
	withServer(t, srv)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "nova"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgYAML := "context_limit: 40\ncompaction:\n  keep_recent: 1\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "nova", "config.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "compact.jsonl")
	st, err := session.Open(path, session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("word ", 200)
	for _, m := range []message.Message{message.User(long), message.Assistant(long)} {
		msg := m
		if _, err := st.Append(session.Entry{Type: session.TypeMessage, Message: &msg}); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	if code, _, errOut := runCLI(t, "--cwd", t.TempDir(), "--session", path, "-p", "go"); code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, errOut)
	}
	reqs := srv.Requests()
	if len(reqs) < 2 {
		t.Fatalf("want a summarise request then the run request, got %d", len(reqs))
	}
	if body := string(reqs[len(reqs)-1].Body); !strings.Contains(body, "summary") {
		t.Fatalf("the run request after compaction should carry the summary: %s", body)
	}
}
