package compaction

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
)

// fakeSummariser is a scripted provider for compaction tests.
type fakeSummariser struct {
	mu        sync.Mutex
	responses []string
	errs      []error
	reqs      []provider.Request
	i         int
	block     bool
}

func newFake(responses ...string) *fakeSummariser {
	return &fakeSummariser{responses: responses}
}

func (f *fakeSummariser) Name() string { return "fake" }

func (f *fakeSummariser) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, cloneReq(req))
	i := f.i
	f.i++
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	text := "summary"
	if i < len(f.responses) {
		text = f.responses[i]
	}
	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamTextDelta, Text: text})
	}
	return &provider.Response{
		Message: message.Assistant(text),
		Usage:   provider.Usage{PromptTokens: 10, CompletionTokens: 5},
	}, nil
}

func (f *fakeSummariser) requests() []provider.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]provider.Request(nil), f.reqs...)
}

func (f *fakeSummariser) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func cloneReq(r provider.Request) provider.Request {
	out := r
	out.Messages = append([]message.Message(nil), r.Messages...)
	return out
}

// promptText joins every message the summariser was sent, for content checks.
func (f *fakeSummariser) promptText(t *testing.T) string {
	t.Helper()
	var b []byte
	for _, r := range f.requests() {
		for _, m := range r.Messages {
			b = append(b, m.Content...)
			b = append(b, '\n')
		}
	}
	return string(b)
}

// longHistory builds n user/assistant turn pairs of ~100 tokens each.
func longHistory(n int) []message.Message {
	var msgs []message.Message
	for i := 0; i < n; i++ {
		msgs = append(msgs, msgTokens(message.RoleUser, 100), msgTokens(message.RoleAssistant, 100))
	}
	return msgs
}

// historySpan is a history guaranteed to have a cut, ending in a tool group.
func historySpan() []message.Message {
	msgs := longHistory(4)
	msgs = append(msgs, message.User("work"))
	msgs = append(msgs, toolGroup("c1")...)
	return msgs
}

// newStore opens an empty session in a temp directory.
func newStore(t *testing.T) *session.Store {
	t.Helper()
	st, err := session.Open(filepath.Join(t.TempDir(), "s.jsonl"), session.Options{})
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// appendTurns appends n user/assistant message entries.
func appendTurns(t *testing.T, st *session.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		u := msgTokens(message.RoleUser, 100)
		if _, err := st.Append(session.Entry{Type: session.TypeMessage, Message: &u}); err != nil {
			t.Fatalf("append: %v", err)
		}
		a := msgTokens(message.RoleAssistant, 100)
		if _, err := st.Append(session.Entry{Type: session.TypeMessage, Message: &a}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
}
