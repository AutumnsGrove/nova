package mode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/permission"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
)

// decodeArray decodes a response's data into a []map.
func decodeArray(t *testing.T, resp proto.Response) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		t.Fatalf("decode %s: %v (data=%s)", resp.Command, err, resp.Data)
	}
	return out
}

func TestHandlers_SVA01_ListModelsCurrent(t *testing.T) {
	models := provider.Models()
	if len(models) == 0 {
		t.Fatal("empty catalog")
	}
	s, _ := testServer(t)
	s.cfg.Model = models[0].ID
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdListModels})
	if !resp.OK {
		t.Fatalf("list_models = %+v", resp)
	}
	rows := decodeArray(t, resp)
	if len(rows) != len(models) {
		t.Fatalf("got %d models, want %d", len(rows), len(models))
	}
	current := 0
	for _, r := range rows {
		if r["id"] == models[0].ID {
			if r["current"] != true {
				t.Fatalf("model %s is not marked current", models[0].ID)
			}
			current++
		}
	}
	if current != 1 {
		t.Fatalf("current markers = %d, want 1", current)
	}
	if _, ok := rows[0]["context_window"]; !ok {
		t.Fatalf("context_window missing: %v", rows[0])
	}
}

func TestHandlers_SVA02_ListModelsQuery(t *testing.T) {
	s, _ := testServer(t)
	full := decodeArray(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdListModels}))
	if len(full) < 2 {
		t.Skip("catalog too small to filter")
	}
	id, _ := full[0]["id"].(string)
	upper := strings.ToUpper(id)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdListModels, Text: upper})
	rows := decodeArray(t, resp)
	if len(rows) == 0 || len(rows) >= len(full) {
		t.Fatalf("filter %q returned %d of %d", upper, len(rows), len(full))
	}
	for _, r := range rows {
		got, _ := r["id"].(string)
		if !strings.Contains(strings.ToLower(got), strings.ToLower(id)) {
			t.Fatalf("filter %q returned unrelated %q", id, got)
		}
	}
	none := decodeArray(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdListModels, Text: "zzz-no-such-model"}))
	if len(none) != 0 {
		t.Fatalf("nonsense query returned %d rows", len(none))
	}
}

func TestHandlers_SVA03_SessionListOrder(t *testing.T) {
	s, _ := testServer(t)
	dir := session.DefaultDir(s.cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	older, newer := filepath.Join(dir, "older.jsonl"), filepath.Join(dir, "newer.jsonl")
	writeSession(t, older, "old")
	writeSession(t, newer, "new")
	base := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(older, base, base)
	_ = os.Chtimes(newer, base.Add(time.Hour), base.Add(time.Hour))
	s.mu.Lock()
	s.path = newer
	s.mu.Unlock()

	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSessionList})
	rows := decodeArray(t, resp)
	if len(rows) < 2 {
		t.Fatalf("got %d sessions, want >= 2", len(rows))
	}
	if rows[0]["path"] != newer {
		t.Fatalf("first session = %v, want newest %s", rows[0]["path"], newer)
	}
	if rows[0]["current"] != true {
		t.Fatalf("newest session is not marked current: %v", rows[0])
	}
}

func TestHandlers_SVA04_GetExtensions(t *testing.T) {
	s, _ := testServer(t)
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetExtensions})
	if !resp.OK || string(resp.Data) != "[]" {
		t.Fatalf("get_extensions without a host = %+v data=%s, want []", resp, resp.Data)
	}

	withExt := newExtServer(t, []string{writeDeclExt(t, "decl_tool_sva04")})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows := decodeArray(t, withExt.Handle(context.Background(), proto.Command{Type: proto.CmdGetExtensions}))
		if len(rows) > 0 {
			if rows[0]["name"] != "decl" {
				t.Fatalf("extension status = %v", rows[0])
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("get_extensions never listed the loaded extension")
}

func TestSession_SVA05_RememberSessionAddsAllow(t *testing.T) {
	s, _ := testServer(t)
	c := NewPipe(s, "ui.confirm")
	defer c.Close()

	q := agent.Question{Tool: "write"}
	first := make(chan bool, 1)
	go func() {
		allow, _ := s.ask(context.Background(), q)
		first <- allow
	}()
	req := <-c.Requests()
	if req.Method != "ui.confirm" || !req.Remember {
		t.Fatalf("request = %+v, want a rememberable confirm", req)
	}
	c.Answer(proto.UIResponse{Type: proto.FrameUIResponse, OK: true, Remember: "session"})
	if !<-first {
		t.Fatal("the answer should allow the first call")
	}

	second := make(chan bool, 1)
	go func() {
		allow, _ := s.ask(context.Background(), q)
		second <- allow
	}()
	select {
	case allow := <-second:
		if !allow {
			t.Fatal("the session rule should allow the second call")
		}
	case req := <-c.Requests():
		t.Fatalf("asked again for an allowed tool: %+v", req)
	}
}

func TestSession_SVA06_RememberNotPersisted(t *testing.T) {
	s, _ := testServer(t)
	s.allowSession("write")
	if !s.sessionAllowed("write") {
		t.Fatal("session rule missing")
	}
	// A fresh server over the same config is a restart: the rule must be gone.
	restarted, _ := testServer(t)
	if restarted.sessionAllowed("write") {
		t.Fatal("the session rule leaked across a restart")
	}
}

func TestSession_SVA07_DenyStillWins(t *testing.T) {
	s, _ := testServer(t)
	s.allowSession("bash")
	s.policy = permission.Policy{Default: permission.Allow, Rules: []permission.Rule{
		{Tool: "bash", Action: permission.Deny},
	}}
	d := s.policy.Decide(permission.Call{Tool: "bash"}, true)
	if d.Action != permission.Deny {
		t.Fatalf("decision = %s, want deny; a session allow must not beat a deny rule", d.Action)
	}
}

func TestSession_SVA08_RememberIgnoredElsewhere(t *testing.T) {
	s, _ := testServer(t)
	c := NewPipe(s, "ui.select")
	defer c.Close()
	go func() {
		_, _ = s.callUI(context.Background(), proto.UIRequest{Type: proto.FrameUIRequest, ID: "u", Method: "select"})
	}()
	req := <-c.Requests()
	if req.Method != "select" {
		t.Fatalf("method = %q", req.Method)
	}
	c.Answer(proto.UIResponse{Type: proto.FrameUIResponse, ID: req.ID, OK: true, IDs: []string{"a"}, Remember: "session"})
	time.Sleep(20 * time.Millisecond)
	if len(s.sessionAllow) != 0 {
		t.Fatalf("remember on a select added rules: %v", s.sessionAllow)
	}
}

func TestSession_SVA09_RememberWithoutOK(t *testing.T) {
	s, _ := testServer(t)
	c := NewPipe(s, "ui.confirm")
	defer c.Close()
	q := agent.Question{Tool: "write"}
	done := make(chan bool, 1)
	go func() {
		allow, _ := s.ask(context.Background(), q)
		done <- allow
	}()
	req := <-c.Requests()
	c.Answer(proto.UIResponse{Type: proto.FrameUIResponse, ID: req.ID, OK: false, Remember: "session"})
	if <-done {
		t.Fatal("ok:false must not allow the call")
	}
	if s.sessionAllowed("write") {
		t.Fatal("remember with ok:false must not add a session rule")
	}
}

// writeSession creates a one-entry session file.
func writeSession(t *testing.T, path, name string) {
	t.Helper()
	st, err := session.Open(path, session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Append(session.Entry{Type: session.TypeSessionInfo, Name: name}); err != nil {
		t.Fatal(err)
	}
}
