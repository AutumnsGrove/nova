package ask

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tool"
)

// fakeUI records requests and returns a scripted response.
type fakeUI struct {
	mu    sync.Mutex
	reqs  []proto.UIRequest
	resp  proto.UIResponse
	err   error
	block chan struct{}
}

func (u *fakeUI) Request(ctx context.Context, req proto.UIRequest) (proto.UIResponse, error) {
	u.mu.Lock()
	u.reqs = append(u.reqs, req)
	block := u.block
	u.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return proto.UIResponse{}, ctx.Err()
		}
	}
	return u.resp, u.err
}

func (u *fakeUI) Notify(proto.UIRequest) {}

func (u *fakeUI) last() proto.UIRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.reqs[len(u.reqs)-1]
}

func setup(ui ext.UIBridge, hasForm bool) *Extension {
	e := New()
	env := &ext.Env{
		UI:     ui,
		HasUI:  func(string) bool { return hasForm },
		Tool:   func(tool.Tool) error { return nil },
		Events: func(func(proto.Event)) {},
	}
	_ = e.Init(env)
	return e
}

func run(t *testing.T, e *Extension, body string) tool.Result {
	t.Helper()
	res, err := e.run(context.Background(), json.RawMessage(body), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func TestAsk_ASK01_OneSingleSelect(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{{ID: "scope", Values: []string{"client"}}}}}
	e := setup(ui, true)
	res := run(t, e, `{"questions":[{"id":"scope","prompt":"Which scope?","kind":"single","options":[{"id":"client","label":"Only the client package"},{"id":"all","label":"Everything"}]}]}`)
	if !strings.Contains(res.Content, "Q1 [scope] Which scope?") || !strings.Contains(res.Content, "Answer: Only the client package") {
		t.Fatalf("result = %q", res.Content)
	}
	req := ui.last()
	if req.Method != "form" || len(req.Questions) != 1 {
		t.Fatalf("request = %+v", req)
	}
}

func TestAsk_ASK02_MultiAndText(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{
		{ID: "parts", Values: []string{"a", "b"}},
		{ID: "why", Value: "because"},
	}}}
	e := setup(ui, true)
	res := run(t, e, `{"questions":[{"id":"parts","prompt":"Which parts?","kind":"multi","options":[{"id":"a","label":"Alpha"},{"id":"b","label":"Beta"}]},{"id":"why","prompt":"Why?","kind":"text"}]}`)
	if !strings.Contains(res.Content, "Answer: Alpha, Beta") || !strings.Contains(res.Content, "Answer: because") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestAsk_ASK03_Other(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{{ID: "scope", Value: "something else"}}}}
	e := setup(ui, true)
	res := run(t, e, `{"questions":[{"id":"scope","prompt":"Which scope?","kind":"single","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}]}]}`)
	if !strings.Contains(res.Content, "Answer: Other: something else") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestAsk_ASK04_NoteVerbatim(t *testing.T) {
	note := "line one\nline two — ünïcode"
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{{ID: "q", Values: []string{"a"}, Notes: note}}}}
	e := setup(ui, true)
	res := run(t, e, `{"questions":[{"id":"q","prompt":"Pick","kind":"single","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}]}]}`)
	if !strings.Contains(res.Content, "Note: "+note) {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestAsk_ASK05_Skipped(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{{ID: "q", Skipped: true}}}}
	e := setup(ui, true)
	res := run(t, e, `{"questions":[{"id":"q","prompt":"Pick","kind":"single","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}]}]}`)
	if !strings.Contains(res.Content, "Answer: skipped") {
		t.Fatalf("result = %q", res.Content)
	}
}

func TestAsk_ASK06_CancelledForm(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Cancelled: true}}
	e := setup(ui, true)
	res := run(t, e, `{"questions":[{"id":"q","prompt":"Pick","kind":"text"}]}`)
	if res.IsError || !strings.Contains(res.Content, dismissedAnswer) {
		t.Fatalf("result = %q isErr=%v", res.Content, res.IsError)
	}
}

func TestAsk_ASK07_NoFormCapability(t *testing.T) {
	e := setup(&fakeUI{}, false)
	res := run(t, e, `{"questions":[{"id":"q","prompt":"Pick","kind":"text"}]}`)
	if !res.IsError || !strings.Contains(res.Content, noUserMessage) {
		t.Fatalf("result = %q isErr=%v", res.Content, res.IsError)
	}
}

func TestAsk_ASK08_PrintMode(t *testing.T) {
	e := setup(nil, false)
	res := run(t, e, `{"questions":[{"id":"q","prompt":"Pick","kind":"text"}]}`)
	if !res.IsError || !strings.Contains(res.Content, noUserMessage) {
		t.Fatalf("result = %q isErr=%v", res.Content, res.IsError)
	}
}

func TestAsk_ASK09_Limits(t *testing.T) {
	ui := &fakeUI{}
	e := setup(ui, true)
	var qs []string
	for i := 0; i < 9; i++ {
		qs = append(qs, `{"id":"q`+string(rune('a'+i))+`","prompt":"p","kind":"text"}`)
	}
	res := run(t, e, `{"questions":[`+strings.Join(qs, ",")+`]}`)
	if !res.IsError || !strings.Contains(res.Content, "limit") {
		t.Fatalf("9 questions result = %q", res.Content)
	}
	var opts []string
	for i := 0; i < 9; i++ {
		opts = append(opts, `{"id":"o`+string(rune('a'+i))+`","label":"L"}`)
	}
	res = run(t, e, `{"questions":[{"id":"q","prompt":"p","kind":"single","options":[`+strings.Join(opts, ",")+`]}]}`)
	if !res.IsError || !strings.Contains(res.Content, "limit") {
		t.Fatalf("9 options result = %q", res.Content)
	}
}

func TestAsk_ASK10_DuplicateOrEmptyIDs(t *testing.T) {
	ui := &fakeUI{}
	e := setup(ui, true)
	res := run(t, e, `{"questions":[{"id":"q","prompt":"p","kind":"text"},{"id":"q","prompt":"p","kind":"text"}]}`)
	if !res.IsError || !strings.Contains(res.Content, "duplicate") {
		t.Fatalf("duplicate result = %q", res.Content)
	}
	res = run(t, e, `{"questions":[{"id":"","prompt":"p","kind":"text"}]}`)
	if !res.IsError || !strings.Contains(res.Content, "non-empty") {
		t.Fatalf("empty result = %q", res.Content)
	}
}

func TestAsk_ASK11_SingleWithOneOptionBecomesText(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{{ID: "q", Value: "typed"}}}}
	e := setup(ui, true)
	run(t, e, `{"questions":[{"id":"q","prompt":"p","kind":"single","options":[{"id":"a","label":"A"}]}]}`)
	if got := ui.last().Questions[0].Kind; got != kindText {
		t.Fatalf("kind = %q, want text", got)
	}
}

func TestAsk_ASK12_LongPromptAndLabels(t *testing.T) {
	ui := &fakeUI{}
	e := setup(ui, true)
	long := strings.Repeat("p", 501)
	res := run(t, e, `{"questions":[{"id":"q","prompt":"`+long+`","kind":"text"}]}`)
	if !res.IsError || !strings.Contains(res.Content, "500") {
		t.Fatalf("prompt result = %q", res.Content)
	}
	label := strings.Repeat("l", 101)
	res = run(t, e, `{"questions":[{"id":"q","prompt":"p","kind":"single","options":[{"id":"a","label":"`+label+`"},{"id":"b","label":"B"}]}]}`)
	if !res.IsError || !strings.Contains(res.Content, "100") {
		t.Fatalf("label result = %q", res.Content)
	}
}

func TestAsk_ASK13_DescriptionAuthoritative(t *testing.T) {
	var registered tool.Tool
	env := &ext.Env{UI: &fakeUI{}, HasUI: func(string) bool { return true }, Tool: func(t tool.Tool) error { registered = t; return nil }}
	e2 := New()
	_ = e2.Init(env)
	if !strings.Contains(registered.Info().Description, "authoritative") {
		t.Fatalf("description = %q", registered.Info().Description)
	}
}

func TestAsk_ASK14_AnswersInOrder(t *testing.T) {
	ui := &fakeUI{resp: proto.UIResponse{Answers: []proto.UIAnswer{
		{ID: "second", Value: "b"},
		{ID: "first", Value: "a"},
	}}}
	e := setup(ui, true)
	res := run(t, e, `{"questions":[{"id":"first","prompt":"F","kind":"text"},{"id":"second","prompt":"S","kind":"text"}]}`)
	if strings.Index(res.Content, "Q1 [first]") > strings.Index(res.Content, "Q2 [second]") {
		t.Fatalf("out of order: %q", res.Content)
	}
}

func TestAsk_ASK15_NoTimeout(t *testing.T) {
	block := make(chan struct{})
	ui := &fakeUI{block: block}
	e := setup(ui, true)
	done := make(chan tool.Result, 1)
	go func() {
		res, _ := e.run(context.Background(), json.RawMessage(`{"questions":[{"id":"q","prompt":"p","kind":"text"}]}`), nil)
		done <- res
	}()
	// Wait until the request is in flight, then confirm no timeout is set.
	for {
		ui.mu.Lock()
		n := len(ui.reqs)
		ui.mu.Unlock()
		if n > 0 {
			break
		}
	}
	if ui.last().TimeoutMS != nil {
		t.Fatal("ask set a timeout")
	}
	select {
	case <-done:
		t.Fatal("ask returned without an answer")
	default:
	}
	close(block)
	<-done
}

func TestAsk_ASK16_AbortWhileWaiting(t *testing.T) {
	ui := &fakeUI{block: make(chan struct{})}
	e := setup(ui, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan tool.Result, 1)
	go func() {
		res, _ := e.run(ctx, json.RawMessage(`{"questions":[{"id":"q","prompt":"p","kind":"text"}]}`), nil)
		done <- res
	}()
	for {
		ui.mu.Lock()
		n := len(ui.reqs)
		ui.mu.Unlock()
		if n > 0 {
			break
		}
	}
	cancel()
	select {
	case res := <-done:
		if !res.IsError || !strings.Contains(res.Content, "cancelled") {
			t.Fatalf("result = %q", res.Content)
		}
	case <-context.Background().Done():
	}
}

// FuzzAskFormat formats arbitrary answers and checks no panic and that every
// question appears.
// The form returns free text instead of option ids when the user picks Other, on
// a multi-choice question as much as on a single one. The model must see it.
func TestAsk_ASK17_OtherTextOnAMultiChoiceQuestion(t *testing.T) {
	questions := []question{{ID: "b", Prompt: "B", Kind: kindMulti, Options: []option{{ID: "p", Label: "P"}, {ID: "q", Label: "Q"}}}}
	cases := map[string]struct {
		answer proto.UIAnswer
		want   string
	}{
		"other only":     {proto.UIAnswer{ID: "b", Value: "something else"}, "Answer: Other: something else"},
		"options+other":  {proto.UIAnswer{ID: "b", Values: []string{"p"}, Value: "more"}, "Answer: P, Other: more"},
		"options only":   {proto.UIAnswer{ID: "b", Values: []string{"p", "q"}}, "Answer: P, Q"},
		"unknown option": {proto.UIAnswer{ID: "b", Values: []string{"zz"}}, "Answer: zz"},
	}
	for name, c := range cases {
		out := formatAnswers(questions, proto.UIResponse{Answers: []proto.UIAnswer{c.answer}})
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: %q does not contain %q", name, out, c.want)
		}
	}
}

func FuzzAskFormat(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		questions := []question{
			{ID: "a", Prompt: "A", Kind: kindSingle, Options: []option{{ID: "x", Label: "X"}, {ID: "y", Label: "Y"}}},
			{ID: "b", Prompt: "B", Kind: kindMulti, Options: []option{{ID: "p", Label: "P"}, {ID: "q", Label: "Q"}}},
			{ID: "c", Prompt: "C", Kind: kindText},
		}
		resp := proto.UIResponse{}
		for i, b := range data {
			id := []string{"a", "b", "c"}[i%3]
			switch b % 4 {
			case 0:
				resp.Answers = append(resp.Answers, proto.UIAnswer{ID: id, Skipped: true})
			case 1:
				resp.Answers = append(resp.Answers, proto.UIAnswer{ID: id, Values: []string{"x", "p"}})
			case 2:
				resp.Answers = append(resp.Answers, proto.UIAnswer{ID: id, Value: "free"})
			case 3:
				resp.Answers = append(resp.Answers, proto.UIAnswer{ID: id, Notes: "n\n"})
			}
		}
		out := formatAnswers(questions, resp)
		for _, q := range questions {
			if !strings.Contains(out, "["+q.ID+"]") {
				t.Fatalf("question %s missing from %q", q.ID, out)
			}
		}
	})
}
