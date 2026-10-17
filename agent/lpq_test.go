package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// steerProvider calls steer once on its first turn, then emits text each turn.
type steerProvider struct {
	steer func()
	turn  int
}

func (p *steerProvider) Name() string { return "steer" }

func (p *steerProvider) Stream(_ context.Context, _ provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	p.turn++
	if p.turn == 1 && p.steer != nil {
		p.steer()
	}
	text := fmt.Sprintf("t%d", p.turn)
	onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: text})
	return &provider.Response{Message: message.Assistant(text)}, nil
}

func Test_LPQ01_SteerDuringTool(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var a *Agent
	tl := toolOf("steer", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		_ = a.Steer(message.User("steered"))
		return tool.Text("ok"), nil
	})
	p := testkit.NewProvider(clock, namedToolTurn("steer"), textTurn("done"))
	a = New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(tl)})
	res, err := a.Run(context.Background(), nil, message.User("go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Turns != 2 || !requestHas(p, 1, "steered") {
		t.Fatalf("steering not injected before the next request: turns=%d", res.Turns)
	}
}

func requestHas(p *testkit.Provider, index int, content string) bool {
	reqs := p.Requests()
	if index >= len(reqs) {
		return false
	}
	for _, m := range reqs[index].Messages {
		if m.Content == content {
			return true
		}
	}
	return false
}

func Test_LPQ02_SteerDuringStreaming(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := &steerProvider{}
	c := &collector{}
	a := New(Config{Provider: p, Model: "m", Clock: clock, Emit: c.emit})
	p.steer = func() { _ = a.Steer(message.User("stream-steer")) }
	res, err := a.Run(context.Background(), nil, message.User("go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Turns != 2 {
		t.Fatalf("turns = %d, want 2", res.Turns)
	}
	if !messageHas(res.Messages, "stream-steer") {
		t.Fatalf("stream steering not injected: %+v", res.Messages)
	}
}

func messageHas(msgs []message.Message, content string) bool {
	for _, m := range msgs {
		if m.Content == content {
			return true
		}
	}
	return false
}

func Test_LPQ03_SeveralSteeringMessages(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	var a *Agent
	tl := toolOf("steer", false, func(context.Context, json.RawMessage, *tool.Context) (tool.Result, error) {
		_ = a.Steer(message.User("s1"))
		_ = a.Steer(message.User("s2"))
		return tool.Text("ok"), nil
	})
	p := testkit.NewProvider(clock, namedToolTurn("steer"), textTurn("done"))
	a = New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(tl)})
	res, err := a.Run(context.Background(), nil, message.User("go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := p.Requests()
	if len(reqs) < 2 {
		t.Fatal("no second request")
	}
	var got []string
	for _, m := range reqs[1].Messages {
		if m.Content == "s1" || m.Content == "s2" {
			got = append(got, m.Content)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{"s1", "s2"}) {
		t.Fatalf("steering order = %v, want [s1 s2]", got)
	}
	_ = res
}

func Test_LPQ04_SteerAfterFinalAnswer(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("final"), textTurn("continued"))
	var a *Agent
	steered := false
	cfg := Config{Provider: p, Model: "m", Clock: clock, OnMessage: func(m message.Message, _ *provider.Usage) error {
		if m.Role == message.RoleAssistant && !steered {
			steered = true
			_ = a.Steer(message.User("late"))
		}
		return nil
	}}
	a = New(cfg)
	res, err := a.Run(context.Background(), nil, message.User("go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Turns != 2 || res.Answer != "continued" {
		t.Fatalf("result = %+v, want a second turn", res)
	}
}

func Test_LPQ05_FollowUpWhenWouldEnd(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("first"), textTurn("second"))
	var a *Agent
	queued := false
	cfg := Config{Provider: p, Model: "m", Clock: clock, OnMessage: func(m message.Message, _ *provider.Usage) error {
		if m.Role == message.RoleAssistant && !queued {
			queued = true
			_ = a.FollowUp(message.User("more"))
		}
		return nil
	}}
	a = New(cfg)
	res, err := a.Run(context.Background(), nil, message.User("go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Turns != 2 || !messageHas(res.Messages, "more") {
		t.Fatalf("follow-up not run: %+v", res)
	}
}

func Test_LPQ06_TwoFollowUpsInOrder(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("one"), textTurn("two"), textTurn("three"))
	var a *Agent
	queued := 0
	cfg := Config{Provider: p, Model: "m", Clock: clock, OnMessage: func(m message.Message, _ *provider.Usage) error {
		if m.Role == message.RoleAssistant && queued < 2 {
			queued++
			_ = a.FollowUp(message.User(fmt.Sprintf("f%d", queued)))
		}
		return nil
	}}
	a = New(cfg)
	res, err := a.Run(context.Background(), nil, message.User("go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Turns != 3 {
		t.Fatalf("turns = %d, want 3", res.Turns)
	}
	var got []string
	for _, m := range res.Messages {
		if m.Content == "f1" || m.Content == "f2" {
			got = append(got, m.Content)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{"f1", "f2"}) {
		t.Fatalf("follow-up order = %v, want [f1 f2]", got)
	}
}

func Test_LPQ07_SteeringBeforeFollowUp(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	p := testkit.NewProvider(clock, textTurn("one"), textTurn("two"), textTurn("three"))
	var a *Agent
	queued := false
	cfg := Config{Provider: p, Model: "m", Clock: clock, OnMessage: func(m message.Message, _ *provider.Usage) error {
		if m.Role == message.RoleAssistant && !queued {
			queued = true
			_ = a.Steer(message.User("s"))
			_ = a.FollowUp(message.User("f"))
		}
		return nil
	}}
	a = New(cfg)
	res, err := a.Run(context.Background(), nil, message.User("go"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	sIdx, fIdx := -1, -1
	for i, m := range res.Messages {
		if m.Content == "s" {
			sIdx = i
		}
		if m.Content == "f" {
			fIdx = i
		}
	}
	if sIdx < 0 || fIdx < 0 || sIdx > fIdx {
		t.Fatalf("steering %d follow-up %d, want steering first: %+v", sIdx, fIdx, res.Messages)
	}
}

func Test_LPQ08_QueueWhenIdle(t *testing.T) {
	a := New(Config{Provider: testkit.NewProvider(testkit.NewFakeClock(baseTime)), Model: "m"})
	if err := a.Steer(message.User("x")); err != ErrNotRunning {
		t.Fatalf("Steer err = %v, want ErrNotRunning", err)
	}
	if err := a.FollowUp(message.User("x")); err != ErrNotRunning {
		t.Fatalf("FollowUp err = %v, want ErrNotRunning", err)
	}
}

func Test_LPQ09_QueueUpdateEvents(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	started := make(chan struct{}, 1)
	p := testkit.NewProvider(clock, namedToolTurn("hang"))
	c := &collector{}
	a := New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(hangingTool(started)), Emit: c.emit})
	done := make(chan runOutcome, 1)
	go func() { done <- runAsyncCtx(a, context.Background()) }()
	<-started
	if err := a.Steer(message.User("s")); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if err := a.FollowUp(message.User("f")); err != nil {
		t.Fatalf("FollowUp: %v", err)
	}
	a.Abort()
	<-done
	if got := c.count(proto.EventQueueUpdate); got != 2 {
		t.Fatalf("queue_update count = %d, want 2", got)
	}
}

func Test_LPQ10_AbortReturnsQueued(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	started := make(chan struct{}, 1)
	p := testkit.NewProvider(clock, namedToolTurn("hang"))
	a := New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(hangingTool(started))})
	done := make(chan struct{})
	go func() {
		_, _ = a.Run(context.Background(), nil, message.User("go"))
		close(done)
	}()
	<-started
	_ = a.Steer(message.User("s1"))
	_ = a.Steer(message.User("s2"))
	_ = a.FollowUp(message.User("f1"))
	steering, followUp := a.Abort()
	if fmt.Sprint(steering) != fmt.Sprint([]string{"s1", "s2"}) {
		t.Fatalf("steering = %v", steering)
	}
	if fmt.Sprint(followUp) != fmt.Sprint([]string{"f1"}) {
		t.Fatalf("followUp = %v", followUp)
	}
	<-done
	if s, f := a.Queued(); s != 0 || f != 0 {
		t.Fatalf("queues not emptied: %d %d", s, f)
	}
}

func Test_LPQ11_AbortIdleAndTwice(t *testing.T) {
	a := New(Config{Provider: testkit.NewProvider(testkit.NewFakeClock(baseTime)), Model: "m"})
	if s, f := a.Abort(); len(s) != 0 || len(f) != 0 {
		t.Fatalf("idle Abort = %v %v", s, f)
	}
	if s, f := a.Abort(); len(s) != 0 || len(f) != 0 {
		t.Fatalf("second Abort = %v %v", s, f)
	}
}

func Test_LPQ12_ConcurrentQueue(t *testing.T) {
	clock := testkit.NewFakeClock(baseTime)
	started := make(chan struct{}, 1)
	p := testkit.NewProvider(clock, namedToolTurn("hang"))
	a := New(Config{Provider: p, Model: "m", Clock: clock, Tools: registry(hangingTool(started))})
	done := make(chan struct{})
	go func() {
		_, _ = a.Run(context.Background(), nil, message.User("go"))
		close(done)
	}()
	<-started

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = a.Steer(message.User(fmt.Sprintf("s%d", i)))
			_ = a.FollowUp(message.User(fmt.Sprintf("f%d", i)))
		}(i)
	}
	wg.Wait()
	steering, followUp := a.Abort()
	<-done

	if len(steering) != n || len(followUp) != n {
		t.Fatalf("got %d steering, %d follow-up, want %d each", len(steering), len(followUp), n)
	}
	seen := map[string]bool{}
	for _, s := range steering {
		if seen[s] {
			t.Fatalf("duplicate steering %q", s)
		}
		seen[s] = true
	}
}
