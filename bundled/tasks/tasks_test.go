package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tool"
)

// fakeSession is a session with a switchable active branch.
type fakeSession struct {
	mu       sync.Mutex
	branches [][]json.RawMessage // one State list per branch
	active   int
	kept     []struct {
		kind string
		data json.RawMessage
		keep bool
	}
}

func (s *fakeSession) AppendEntry(kind string, data json.RawMessage, keep bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kept = append(s.kept, struct {
		kind string
		data json.RawMessage
		keep bool
	}{kind, append(json.RawMessage(nil), data...), keep})
	if len(s.branches) == 0 {
		s.branches = [][]json.RawMessage{{}}
	}
	s.branches[s.active] = append(s.branches[s.active], append(json.RawMessage(nil), data...))
	return nil
}

func (s *fakeSession) Entries(kind string) []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind != stateKind || len(s.branches) == 0 {
		return nil
	}
	out := make([]json.RawMessage, len(s.branches[s.active]))
	copy(out, s.branches[s.active])
	return out
}

func (s *fakeSession) push(snap State) {
	data, _ := Encode(State{V: stateVersion, Next: snap.Next, Tasks: snap.Tasks})
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.branches) == 0 {
		s.branches = [][]json.RawMessage{{}}
	}
	s.branches[s.active] = append(s.branches[s.active], data)
}

func (s *fakeSession) newBranch(snap State) {
	data, _ := Encode(State{V: stateVersion, Next: snap.Next, Tasks: snap.Tasks})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.branches = append(s.branches, []json.RawMessage{data})
	s.active = len(s.branches) - 1
}

// fakeUI records widget and status notifications.
type fakeUI struct {
	mu   sync.Mutex
	reqs []proto.UIRequest
}

func (u *fakeUI) Request(_ context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	u.append(req)
	return proto.UIResponse{ID: req.ID}, nil
}

func (u *fakeUI) Notify(req proto.UIRequest) { u.append(req) }

func (u *fakeUI) append(req proto.UIRequest) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.reqs = append(u.reqs, req)
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

// setup builds an extension wired to fakes and returns the branch observer.
func setup(sess ext.SessionAPI, ui ext.UIBridge) (*Extension, func(proto.Event)) {
	e := New()
	var obs func(proto.Event)
	env := &ext.Env{
		Session: sess,
		UI:      ui,
		Tool:    func(tool.Tool) error { return nil },
		Command: func(ext.InProcCommand) error { return nil },
		Events:  func(fn func(proto.Event)) { obs = fn },
	}
	_ = e.Init(env)
	return e, obs
}

// call runs one todo action and returns the result text.
func call(t *testing.T, e *Extension, body string) (tool.Result, bool) {
	t.Helper()
	res, err := e.run(context.Background(), json.RawMessage(body), nil)
	if err != nil {
		t.Fatalf("run(%s): %v", body, err)
	}
	return res, res.IsError
}

func mustCreate(t *testing.T, e *Extension, titles ...string) {
	t.Helper()
	for _, title := range titles {
		res, isErr := call(t, e, fmt.Sprintf(`{"action":"create","title":%q}`, title))
		if isErr {
			t.Fatalf("create %q failed: %s", title, res.Content)
		}
	}
}

func TestTasks_TSK01_CreateOne(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	res, isErr := call(t, e, `{"action":"create","title":"Fix the retry"}`)
	if isErr || !strings.Contains(res.Content, "t1 [todo] Fix the retry") {
		t.Fatalf("create = %q isErr=%v", res.Content, isErr)
	}
	if len(e.state.Tasks) != 1 || e.state.Tasks[0].ID != "t1" {
		t.Fatalf("tasks = %+v", e.state.Tasks)
	}
}

func TestTasks_TSK02_CreateMany(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	var items []string
	for i := 1; i <= 50; i++ {
		items = append(items, fmt.Sprintf(`{"title":"task %d"}`, i))
	}
	res, isErr := call(t, e, `{"action":"create","tasks":[`+strings.Join(items, ",")+`]}`)
	if isErr {
		t.Fatalf("create many failed: %s", res.Content)
	}
	if len(e.state.Tasks) != 50 || e.state.Tasks[0].ID != "t1" || e.state.Tasks[49].ID != "t50" {
		t.Fatalf("ids = %s..%s (%d)", e.state.Tasks[0].ID, e.state.Tasks[len(e.state.Tasks)-1].ID, len(e.state.Tasks))
	}
}

func TestTasks_TSK03_TooManyInOneCall(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	var items []string
	for i := 0; i < 51; i++ {
		items = append(items, fmt.Sprintf(`{"title":"task %d"}`, i))
	}
	res, isErr := call(t, e, `{"action":"create","tasks":[`+strings.Join(items, ",")+`]}`)
	if !isErr || !strings.Contains(res.Content, "at most 50") {
		t.Fatalf("result = %q", res.Content)
	}
	if len(e.state.Tasks) != 0 {
		t.Fatalf("nothing should be created, got %d", len(e.state.Tasks))
	}
}

func TestTasks_TSK04_TaskLimit(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	for batch := 0; batch < 4; batch++ {
		var items []string
		for i := 0; i < 50; i++ {
			items = append(items, fmt.Sprintf(`{"title":"t"}`))
		}
		if _, isErr := call(t, e, `{"action":"create","tasks":[`+strings.Join(items, ",")+`]}`); isErr {
			t.Fatalf("batch %d failed", batch)
		}
	}
	res, isErr := call(t, e, `{"action":"create","title":"one more"}`)
	if !isErr || !strings.Contains(res.Content, "at most 200") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestTasks_TSK05_Update(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	mustCreate(t, e, "first")
	call(t, e, `{"action":"update","id":"t1","title":"renamed","status":"doing","detail":"a note"}`)
	if e.state.Tasks[0].Title != "renamed" || e.state.Tasks[0].Status != "doing" || e.state.Tasks[0].Detail != "a note" {
		t.Fatalf("task = %+v", e.state.Tasks[0])
	}
}

func TestTasks_TSK06_UnknownID(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	res, isErr := call(t, e, `{"action":"update","id":"t9","status":"done"}`)
	if !isErr || !strings.Contains(res.Content, "t9") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestTasks_TSK07_BadStatus(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	mustCreate(t, e, "first")
	res, isErr := call(t, e, `{"action":"update","id":"t1","status":"later"}`)
	if !isErr || !strings.Contains(res.Content, "status") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestTasks_TSK08_EmptyTitle(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	for _, title := range []string{"", "   "} {
		res, isErr := call(t, e, fmt.Sprintf(`{"action":"create","title":%q}`, title))
		if !isErr || !strings.Contains(res.Content, "title") {
			t.Fatalf("title %q: result = %q", title, res.Content)
		}
	}
	if len(e.state.Tasks) != 0 {
		t.Fatalf("nothing should be created, got %d", len(e.state.Tasks))
	}
}

func TestTasks_TSK09_FieldLimits(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	long := strings.Repeat("x", 201)
	res, isErr := call(t, e, fmt.Sprintf(`{"action":"create","title":%q}`, long))
	if !isErr || !strings.Contains(res.Content, "200") {
		t.Fatalf("title result = %q", res.Content)
	}
	long = strings.Repeat("y", 2001)
	res, isErr = call(t, e, fmt.Sprintf(`{"action":"create","title":"ok","detail":%q}`, long))
	if !isErr || !strings.Contains(res.Content, "2000") {
		t.Fatalf("detail result = %q", res.Content)
	}
}

func TestTasks_TSK10_Delete(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	mustCreate(t, e, "one", "two")
	call(t, e, `{"action":"delete","id":"t1"}`)
	if len(e.state.Tasks) != 1 || e.state.Tasks[0].ID != "t2" {
		t.Fatalf("tasks = %+v", e.state.Tasks)
	}
}

func TestTasks_TSK11_IDsNeverReused(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	mustCreate(t, e, "one", "two")
	call(t, e, `{"action":"delete","id":"t2"}`)
	mustCreate(t, e, "three")
	if e.state.Tasks[len(e.state.Tasks)-1].ID != "t3" {
		t.Fatalf("new id = %q, want t3", e.state.Tasks[len(e.state.Tasks)-1].ID)
	}
}

func TestTasks_TSK12_ListAndClear(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	mustCreate(t, e, "one", "two")
	res, _ := call(t, e, `{"action":"list"}`)
	if !strings.Contains(res.Content, "t1") || !strings.Contains(res.Content, "t2") {
		t.Fatalf("list = %q", res.Content)
	}
	res, _ = call(t, e, `{"action":"clear"}`)
	if len(e.state.Tasks) != 0 || strings.Contains(res.Content, "t1") {
		t.Fatalf("clear = %q, tasks=%d", res.Content, len(e.state.Tasks))
	}
}

func TestTasks_TSK13_UnknownAction(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	res, isErr := call(t, e, `{"action":"frobnicate"}`)
	if !isErr || !strings.Contains(res.Content, "create, update, delete, list, clear") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestTasks_TSK14_WholeListEveryCall(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	mustCreate(t, e, "one", "two", "three")
	res, _ := call(t, e, `{"action":"update","id":"t2","status":"done"}`)
	for _, id := range []string{"t1", "t2", "t3"} {
		if !strings.Contains(res.Content, id) {
			t.Fatalf("result %q missing %s", res.Content, id)
		}
	}
}

func TestTasks_TSK15_TwoDoing(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	mustCreate(t, e, "one", "two")
	call(t, e, `{"action":"update","id":"t1","status":"doing"}`)
	res, _ := call(t, e, `{"action":"update","id":"t2","status":"doing"}`)
	if !strings.Contains(res.Content, "keep one in progress") {
		t.Fatalf("no reminder: %q", res.Content)
	}
}

func TestTasks_TSK16_SnapshotWritten(t *testing.T) {
	sess := &fakeSession{}
	e, _ := setup(sess, &fakeUI{})
	mustCreate(t, e, "one")
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.kept) == 0 {
		t.Fatal("no State written")
	}
	last := sess.kept[len(sess.kept)-1]
	if last.kind != stateKind || !last.keep {
		t.Fatalf("entry = %+v, want kind %s keep true", last, stateKind)
	}
}

func TestTasks_TSK17_StateRebuiltOnResume(t *testing.T) {
	sess := &fakeSession{}
	e, _ := setup(sess, &fakeUI{})
	mustCreate(t, e, "one", "two")
	e2, _ := setup(sess, &fakeUI{})
	if len(e2.state.Tasks) != 2 || e2.state.Tasks[1].Title != "two" {
		t.Fatalf("rebuilt = %+v", e2.state.Tasks)
	}
}

func TestTasks_TSK18_StateRebuiltOnReload(t *testing.T) {
	sess := &fakeSession{}
	e, _ := setup(sess, &fakeUI{})
	mustCreate(t, e, "one")
	e.rebuild()
	if len(e.state.Tasks) != 1 {
		t.Fatalf("after rebuild = %+v", e.state.Tasks)
	}
}

func TestTasks_TSK19_BranchNavigation(t *testing.T) {
	sess := &fakeSession{}
	e, obs := setup(sess, &fakeUI{})
	mustCreate(t, e, "one")
	// A second branch holds a different State.
	sess.newBranch(State{V: 1, Next: 5, Tasks: []Task{{ID: "t5", Title: "branch task", Status: statusTodo}}})
	obs(proto.Event{Type: proto.EventSessionChanged})
	if len(e.state.Tasks) != 1 || e.state.Tasks[0].ID != "t5" {
		t.Fatalf("after branch change = %+v", e.state.Tasks)
	}
}

func TestTasks_TSK20_StateAfterCompaction(t *testing.T) {
	sess := &fakeSession{}
	e, _ := setup(sess, &fakeUI{})
	mustCreate(t, e, "survivor")
	// Compaction drops non-keep entries; the fake applies the same rule.
	sess.mu.Lock()
	branch := sess.branches[sess.active][:0]
	for _, raw := range sess.branches[sess.active] {
		branch = append(branch, raw)
	}
	sess.branches[sess.active] = branch
	sess.mu.Unlock()
	e.rebuild()
	if len(e.state.Tasks) != 1 || e.state.Tasks[0].Title != "survivor" {
		t.Fatalf("after compaction = %+v", e.state.Tasks)
	}
}

func TestTasks_TSK21_NoSnapshotEmpty(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	if len(e.state.Tasks) != 0 {
		t.Fatalf("tasks = %+v, want empty", e.state.Tasks)
	}
	res, _ := call(t, e, `{"action":"list"}`)
	if res.Content != "(no tasks)" {
		t.Fatalf("list = %q", res.Content)
	}
}

func TestTasks_TSK22_ConcurrentCalls(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = e.run(context.Background(), json.RawMessage(fmt.Sprintf(`{"action":"create","title":"t%d"}`, n)), nil)
		}(i)
	}
	wg.Wait()
	if len(e.state.Tasks) != 20 {
		t.Fatalf("tasks = %d, want 20", len(e.state.Tasks))
	}
	seen := map[string]bool{}
	for _, task := range e.state.Tasks {
		if seen[task.ID] {
			t.Fatalf("duplicate id %s", task.ID)
		}
		seen[task.ID] = true
	}
}

func TestTasks_TSK23_WidgetAndStatus(t *testing.T) {
	ui := &fakeUI{}
	e, _ := setup(&fakeSession{}, ui)
	mustCreate(t, e, "one", "two")
	call(t, e, `{"action":"update","id":"t1","status":"done"}`)
	w, ok := ui.last("widget")
	if !ok || !strings.Contains(string(w.Content), `"Kind":"list"`) {
		t.Fatalf("widget = %+v", w)
	}
	s, ok := ui.last("status")
	if !ok || len(s.Segments) == 0 || s.Segments[0].Text != "☐ 1 of 2" {
		t.Fatalf("status = %+v", s)
	}
}

func TestTasks_TSK24_AllDoneCollapses(t *testing.T) {
	ui := &fakeUI{}
	e, _ := setup(&fakeSession{}, ui)
	mustCreate(t, e, "one", "two")
	call(t, e, `{"action":"update","id":"t1","status":"done"}`)
	call(t, e, `{"action":"update","id":"t2","status":"done"}`)
	w, ok := ui.last("widget")
	if !ok || !strings.Contains(string(w.Content), "☑ 2 of 2 tasks") {
		t.Fatalf("widget = %+v", w)
	}
}

func TestTasks_TSK25_EmptyClears(t *testing.T) {
	ui := &fakeUI{}
	e, _ := setup(&fakeSession{}, ui)
	mustCreate(t, e, "one")
	call(t, e, `{"action":"clear"}`)
	w, ok := ui.last("widget")
	if !ok || len(w.Content) != 0 {
		t.Fatalf("clear widget = %+v", w)
	}
	s, ok := ui.last("status")
	if !ok || len(s.Segments) != 0 {
		t.Fatalf("clear status = %+v", s)
	}
}

func TestTasks_TSK26_TodosCommand(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	mustCreate(t, e, "one")
	out, err := e.command(context.Background(), "")
	if err != nil || !strings.Contains(out, "t1") {
		t.Fatalf("command = %q, %v", out, err)
	}
	out, err = e.command(context.Background(), "clear")
	if err != nil || len(e.state.Tasks) != 0 {
		t.Fatalf("clear = %q, %v tasks=%d", out, err, len(e.state.Tasks))
	}
}

func TestTasks_TSK27_NoUIClient(t *testing.T) {
	e, _ := setup(&fakeSession{}, nil)
	res, isErr := call(t, e, `{"action":"create","title":"one"}`)
	if isErr || !strings.Contains(res.Content, "t1") {
		t.Fatalf("result = %q isErr=%v", res.Content, isErr)
	}
}

func TestTasks_TSK28_UnicodeTitles(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	title := "récupérer les données 🚀"
	mustCreate(t, e, title)
	if e.state.Tasks[0].Title != title {
		t.Fatalf("title = %q", e.state.Tasks[0].Title)
	}
}
