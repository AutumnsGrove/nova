package mode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
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
	"github.com/AutumnsGrove/nova/testkit"
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

// TestUI_UXB09_ExtensionFormRoundTrip drives a form request from an extension
// bridge through a real server and a client, and checks the answers come back.
func TestUI_UXB09_ExtensionFormRoundTrip(t *testing.T) {
	s, _ := testServer(t)
	c := NewPipe(s, "ui.form")
	defer c.Close()

	req := proto.UIRequest{
		Type: proto.FrameUIRequest, ID: "form-1", Method: "form", Title: "Ask",
		Questions: []proto.UIQuestion{{ID: "q1", Prompt: "Say something", Kind: "text"}},
	}
	type result struct {
		resp proto.UIResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := uiBridge{s}.Request(context.Background(), req)
		done <- result{resp, err}
	}()

	got := <-c.Requests()
	if got.ID != "form-1" || got.Method != "form" {
		t.Fatalf("extension request = %+v", got)
	}
	c.Answer(proto.UIResponse{Type: proto.FrameUIResponse, ID: got.ID, OK: true,
		Answers: []proto.UIAnswer{{ID: "q1", Value: "hello"}}})

	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if len(r.resp.Answers) != 1 || r.resp.Answers[0].ID != "q1" || r.resp.Answers[0].Value != "hello" {
		t.Fatalf("extension got answers = %+v", r.resp.Answers)
	}
}

func TestHandlers_SVA10_ListModelsInputModalities(t *testing.T) {
	provider.RegisterModel(provider.Model{ID: "sva10-vision", Name: "Vision", Provider: "test", InputModalities: []string{"text", "image"}})
	provider.RegisterModel(provider.Model{ID: "sva10-text", Name: "Text", Provider: "test"})
	s, _ := testServer(t)
	rows := decodeArray(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdListModels, Text: "sva10-vision"}))
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got, _ := rows[0]["input_modalities"].([]any)
	if len(got) != 2 || got[0] != "text" || got[1] != "image" {
		t.Fatalf("input_modalities = %v, want [text image]", rows[0]["input_modalities"])
	}
	text := decodeArray(t, s.Handle(context.Background(), proto.Command{Type: proto.CmdListModels, Text: "sva10-text"}))
	if len(text) != 1 {
		t.Fatalf("text rows = %d, want 1", len(text))
	}
	if _, ok := text[0]["input_modalities"]; ok {
		t.Fatalf("text-only model should omit input_modalities: %v", text[0])
	}
}

func TestHandlers_SVA11_StateInputModalities(t *testing.T) {
	provider.RegisterModel(provider.Model{ID: "sva11-vision", Name: "Vision", Provider: "test", InputModalities: []string{"text", "image"}})
	s, _ := testServer(t)
	s.cfg.Model = "sva11-vision"
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdGetState})
	var state map[string]any
	if err := json.Unmarshal(resp.Data, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	got, _ := state["input_modalities"].([]any)
	if len(got) != 2 || got[0] != "text" || got[1] != "image" {
		t.Fatalf("input_modalities = %v, want [text image]", state["input_modalities"])
	}
}

func TestHandlers_SVA12_CompleteModelSelection(t *testing.T) {
	provider.RegisterModel(provider.Model{ID: "sva12-vision", Slug: "vision-slug", Provider: "test"})
	s, prov := testServer(t, testkit.Turn{Text: "ok"})
	if _, err := s.complete(context.Background(), provider.Request{ModelID: "sva12-vision"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	reqs := prov.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Model != "vision-slug" {
		t.Errorf("model = %q, want the requested catalog model's slug", reqs[0].Model)
	}
}

// imageAttachment builds a protocol attachment carrying a tiny PNG.
func imageAttachment(t *testing.T) proto.Attachment {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return proto.Attachment{
		Kind:     "image",
		Name:     "shot.png",
		MimeType: "image/png",
		Data:     base64.StdEncoding.EncodeToString(buf.Bytes()),
	}
}

func TestHandlers_SVA13_PromptAttachmentReachesProvider(t *testing.T) {
	provider.RegisterModel(provider.Model{ID: "sva13-vision", Slug: "vision", Provider: "test", InputModalities: []string{"text", "image"}})
	s, prov := testServer(t, testkit.Turn{Text: "ok"})
	s.cfg.Model = "sva13-vision"
	events, cancel := s.Subscribe(64)
	defer cancel()

	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "look", Attachments: []proto.Attachment{imageAttachment(t)}})
	if !resp.OK {
		t.Fatalf("prompt = %+v", resp)
	}
	waitFor(t, events, proto.EventRunEnd)

	reqs := prov.Requests()
	if len(reqs) == 0 {
		t.Fatal("provider saw no request")
	}
	last := reqs[0].Messages[len(reqs[0].Messages)-1]
	if !last.HasImages() {
		t.Fatalf("request last message = %+v, want an image part", last)
	}
}

func TestHandlers_SVA14_TextOnlyModelRejectsPromptImage(t *testing.T) {
	s, _ := testServer(t) // test/model has no modalities, so text only
	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "look", Attachments: []proto.Attachment{imageAttachment(t)}})
	if resp.OK || resp.Error.Code != proto.CodeBadRequest {
		t.Fatalf("prompt = %+v, want bad_request", resp)
	}
	if !strings.Contains(resp.Error.Message, "does not support images") {
		t.Fatalf("message = %q, want a capability error", resp.Error.Message)
	}
}

func TestHandlers_SVA15_SteerAttachmentAccepted(t *testing.T) {
	provider.RegisterModel(provider.Model{ID: "sva15-vision", Slug: "vision", Provider: "test", InputModalities: []string{"text", "image"}})
	s, _ := testServer(t, testkit.Turn{Hang: true})
	s.cfg.Model = "sva15-vision"
	events, cancel := s.Subscribe(64)
	defer cancel()
	s.Handle(context.Background(), proto.Command{Type: proto.CmdPrompt, Text: "one"})
	waitFor(t, events, proto.EventRunStart)

	resp := s.Handle(context.Background(), proto.Command{Type: proto.CmdSteer, Text: "two", Attachments: []proto.Attachment{imageAttachment(t)}})
	if !resp.OK {
		t.Fatalf("steer = %+v, want the attachment queued", resp)
	}
	if steering, _ := s.agent.Queued(); steering != 1 {
		t.Fatalf("steering = %d, want 1", steering)
	}
	s.Handle(context.Background(), proto.Command{Type: proto.CmdAbort})
}
