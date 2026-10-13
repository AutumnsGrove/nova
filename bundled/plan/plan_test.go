package plan

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

// fakeSession stores custom entries and returns the last of a kind.
type fakeSession struct {
	mu      sync.Mutex
	entries map[string]json.RawMessage
}

func newSession() *fakeSession { return &fakeSession{entries: map[string]json.RawMessage{}} }

func (s *fakeSession) AppendEntry(kind string, data json.RawMessage, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[kind] = append(json.RawMessage(nil), data...)
	return nil
}

func (s *fakeSession) Entries(kind string) []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, ok := s.entries[kind]; ok {
		return []json.RawMessage{data}
	}
	return nil
}

// fakeUI records requests and returns a scripted answer.
type fakeUI struct {
	mu   sync.Mutex
	reqs []proto.UIRequest
	resp proto.UIResponse
}

func (u *fakeUI) Request(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	u.mu.Lock()
	u.reqs = append(u.reqs, req)
	u.mu.Unlock()
	return u.resp, nil
}

func (u *fakeUI) Notify(req proto.UIRequest) {
	u.mu.Lock()
	u.reqs = append(u.reqs, req)
	u.mu.Unlock()
}

func (u *fakeUI) last(method string) (proto.UIRequest, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for i := len(u.reqs) - 1; i >= 0; i-- {
		if u.reqs[i].Method == method {
			return u.reqs[i], true
		}
	}
	return proto.UIRequest{}, false
}

// fakeRegistry reports tool annotations.
type fakeRegistry struct{ infos map[string]tool.Info }

func (r fakeRegistry) Info(name string) (tool.Info, bool) { i, ok := r.infos[name]; return i, ok }

func setup(sess *fakeSession, ui ext.UIBridge, extra map[string]any) *Extension {
	e := New()
	if extra == nil {
		extra = map[string]any{}
	}
	env := &ext.Env{
		Session: sess,
		UI:      ui,
		Extra:   extra,
		HasUI:   func(string) bool { return ui != nil },
		Tool:    func(tool.Tool) error { return nil },
		Command: func(ext.InProcCommand) error { return nil },
		Hooks:   func(agent.Hooks) {},
		Events:  func(func(proto.Event)) {},
	}
	_ = e.Init(env)
	return e
}

func TestPlan_PLN01_Toggle(t *testing.T) {
	ui := &fakeUI{}
	e := setup(newSession(), ui, nil)
	if out, _ := e.command(context.Background(), ""); !e.on || !strings.Contains(out, "on") {
		t.Fatalf("toggle on: on=%v out=%q", e.on, out)
	}
	seg, ok := ui.last("status")
	if !ok || len(seg.Segments) == 0 || seg.Segments[0].Text != "plan" {
		t.Fatalf("segment = %+v", seg)
	}
	if _, err := e.command(context.Background(), ""); err != nil || e.on {
		t.Fatalf("toggle off: on=%v", e.on)
	}
}

func TestPlan_PLN02_StatePersisted(t *testing.T) {
	sess := newSession()
	e := setup(sess, &fakeUI{}, nil)
	e.command(context.Background(), "")
	e2 := setup(sess, &fakeUI{}, nil)
	if !e2.on {
		t.Fatal("plan mode not rebuilt from the session")
	}
}

func TestPlan_PLN03_NonReadOnlyBlocked(t *testing.T) {
	reg := fakeRegistry{infos: map[string]tool.Info{"write": {Name: "write", ReadOnly: false}}}
	e := setup(newSession(), &fakeUI{}, map[string]any{"tools": reg})
	e.command(context.Background(), "")
	dec := e.hookToolCall(context.Background(), agent.ToolCall{Name: "write"})
	if dec == nil || !dec.Block || dec.Reason != blockReason {
		t.Fatalf("decision = %+v", dec)
	}
}

func TestPlan_PLN04_ReadOnlyAllowed(t *testing.T) {
	reg := fakeRegistry{infos: map[string]tool.Info{
		"read": {Name: "read", ReadOnly: true},
		"grep": {Name: "grep", ReadOnly: true},
	}}
	e := setup(newSession(), &fakeUI{}, map[string]any{"tools": reg})
	e.command(context.Background(), "")
	for _, name := range []string{"read", "grep"} {
		if dec := e.hookToolCall(context.Background(), agent.ToolCall{Name: name}); dec != nil {
			t.Fatalf("%s blocked: %+v", name, dec)
		}
	}
}

func TestPlan_PLN05_AskAndSubmitAllowed(t *testing.T) {
	reg := fakeRegistry{infos: map[string]tool.Info{"plan_submit": {ReadOnly: true}, "ask": {ReadOnly: true}}}
	e := setup(newSession(), &fakeUI{}, map[string]any{"tools": reg})
	e.command(context.Background(), "")
	for _, name := range []string{"ask", "plan_submit"} {
		if dec := e.hookToolCall(context.Background(), agent.ToolCall{Name: name}); dec != nil {
			t.Fatalf("%s blocked: %+v", name, dec)
		}
	}
}

func TestPlan_PLN06_SystemInstructions(t *testing.T) {
	e := setup(newSession(), &fakeUI{}, nil)
	e.command(context.Background(), "")
	req := &provider.Request{Messages: []message.Message{message.System("base")}}
	e.hookBeforeProviderRequest(context.Background(), req)
	if !strings.Contains(req.Messages[0].Content, "Plan mode is on") {
		t.Fatalf("system = %q", req.Messages[0].Content)
	}
}

func TestPlan_PLN07_Approve(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{{ID: "plan", Values: []string{"approve"}}}}}
	e := setup(newSession(), ui, nil)
	e.command(context.Background(), "")
	res, _ := e.submit(context.Background(), json.RawMessage(`{"plan":"do the thing"}`), nil)
	if e.on || !strings.Contains(res.Content, approveText) {
		t.Fatalf("on=%v result=%q", e.on, res.Content)
	}
}

func TestPlan_PLN08_Revise(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{{ID: "plan", Values: []string{"revise"}, Notes: "add tests"}}}}
	e := setup(newSession(), ui, nil)
	e.command(context.Background(), "")
	res, _ := e.submit(context.Background(), json.RawMessage(`{"plan":"do the thing"}`), nil)
	if !e.on || !strings.Contains(res.Content, "add tests") || !strings.Contains(res.Content, reviseText) {
		t.Fatalf("on=%v result=%q", e.on, res.Content)
	}
}

func TestPlan_PLN09_Cancel(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{{ID: "plan", Values: []string{"cancel"}}}}}
	e := setup(newSession(), ui, nil)
	e.command(context.Background(), "")
	res, _ := e.submit(context.Background(), json.RawMessage(`{"plan":"do the thing"}`), nil)
	if e.on || !strings.Contains(res.Content, cancelText) {
		t.Fatalf("on=%v result=%q", e.on, res.Content)
	}
}

func TestPlan_PLN10_NoUI(t *testing.T) {
	e := setup(newSession(), nil, nil)
	e.command(context.Background(), "")
	res, _ := e.submit(context.Background(), json.RawMessage(`{"plan":"do the thing"}`), nil)
	if !strings.Contains(res.Content, noUIText) || !e.on {
		t.Fatalf("on=%v result=%q", e.on, res.Content)
	}
	if td := e.hookTurnEnd(context.Background(), agent.Turn{}); !td.End {
		t.Fatalf("turn decision = %+v", td)
	}
	if td := e.hookTurnEnd(context.Background(), agent.Turn{}); td.End {
		t.Fatal("run should end only once")
	}
}

func TestPlan_PLN11_OutsidePlanMode(t *testing.T) {
	e := setup(newSession(), &fakeUI{}, nil)
	res, _ := e.submit(context.Background(), json.RawMessage(`{"plan":"do the thing"}`), nil)
	if !res.IsError || !strings.Contains(res.Content, "not in plan mode") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestPlan_PLN12_PlanSizeLimits(t *testing.T) {
	e := setup(newSession(), &fakeUI{}, nil)
	e.command(context.Background(), "")
	res, _ := e.submit(context.Background(), json.RawMessage(`{"plan":"   "}`), nil)
	if !res.IsError {
		t.Fatalf("empty plan result = %q", res.Content)
	}
	big := strings.Repeat("x", maxPlan+1)
	res, _ = e.submit(context.Background(), json.RawMessage(`{"plan":"`+big+`"}`), nil)
	if !res.IsError || !strings.Contains(res.Content, "20000") {
		t.Fatalf("big plan result = %q", res.Content)
	}
}

func TestPlan_PLN13_ToggleOffMidRun(t *testing.T) {
	reg := fakeRegistry{infos: map[string]tool.Info{"write": {ReadOnly: false}}}
	e := setup(newSession(), &fakeUI{}, map[string]any{"tools": reg})
	e.command(context.Background(), "")
	if dec := e.hookToolCall(context.Background(), agent.ToolCall{Name: "write"}); dec == nil {
		t.Fatal("write should be blocked while on")
	}
	e.command(context.Background(), "")
	if dec := e.hookToolCall(context.Background(), agent.ToolCall{Name: "write"}); dec != nil {
		t.Fatalf("write blocked after toggling off: %+v", dec)
	}
}

func TestPlan_PLN14_PlanFlag(t *testing.T) {
	e := setup(newSession(), &fakeUI{}, map[string]any{"plan": true})
	if !e.on {
		t.Fatal("Extra[plan] did not start plan mode")
	}
}

func TestPlan_PLN15_BlockedCallResult(t *testing.T) {
	reg := fakeRegistry{infos: map[string]tool.Info{"bash": {ReadOnly: false}}}
	e := setup(newSession(), &fakeUI{}, map[string]any{"tools": reg})
	e.command(context.Background(), "")
	dec := e.hookToolCall(context.Background(), agent.ToolCall{Name: "bash"})
	if dec == nil || !dec.Block || dec.Reason == "" {
		t.Fatalf("decision = %+v", dec)
	}
}
