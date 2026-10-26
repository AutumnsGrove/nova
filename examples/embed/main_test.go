package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/testkit"
	"github.com/AutumnsGrove/nova/tool"
)

// demoStore is the app's state with the note the scripted run expects to find.
func demoStore() *Store {
	return &Store{notes: []Note{
		{ID: "n1", Title: "Nova", Body: "one binary, many front doors"},
		{ID: "n2", Title: "Polaris", Body: "first harness consumer"},
	}}
}

// newApp wires the app the way main does, minus config and login.
func newApp(p provider.Provider, store *Store, tools *tool.Registry, events *[]Event) *App {
	var mu sync.Mutex
	return &App{
		Provider: p,
		Model:    "test-model",
		Tools:    tools,
		Store:    store,
		Emit: func(e Event) {
			mu.Lock()
			defer mu.Unlock()
			*events = append(*events, e)
		},
	}
}

// twoToolTurns scripts the conversation the example is meant to show: two tool
// calls in one batch with no text, then a nudged answer.
func twoToolTurns() []testkit.Turn {
	return []testkit.Turn{
		{
			ToolCalls: []testkit.Call{
				{ID: "c1", Name: "note_search", Args: `{"query":"nova"}`},
				{ID: "c2", Name: "note_search", Args: `{"query":"polaris"}`},
			},
			Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CostUSD: 0.001},
		},
		{
			Text:  "Nova is one binary; Polaris is its first consumer.",
			Usage: &provider.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30, CostUSD: 0.002},
		},
	}
}

// TestEmbed_ToolBatchNudge drives the example with a scripted provider and
// asserts on the transcript, the mapped events, and the usage total, which is
// what a consumer reads back from Result.
func TestEmbed_ToolBatchNudge(t *testing.T) {
	clock := testkit.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	p := testkit.NewProvider(clock, twoToolTurns()...)
	store := demoStore()
	var events []Event
	app := newApp(p, store, NoteTools(), &events)

	res, err := app.Run(context.Background(), nil, "what do I know about nova and polaris?", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Reason != "done" {
		t.Fatalf("Reason = %q, want done", res.Reason)
	}
	if !strings.Contains(res.Answer, "Polaris is its first consumer") {
		t.Fatalf("Answer = %q, want the scripted answer", res.Answer)
	}

	// Transcript: prompt, assistant with both calls, one result per call, the
	// nudge, then the answer. Counting the tool messages proves both calls ran.
	if got := countRole(res.Messages, message.RoleTool); got != 2 {
		t.Fatalf("tool messages = %d, want 2\n%+v", got, res.Messages)
	}
	nudge := findUserAfterLastTool(res.Messages)
	if !strings.Contains(nudge, "Report what you found") {
		t.Fatalf("nudge after the tool batch = %q, want the TurnEnd inject", nudge)
	}
	if last := res.Messages[len(res.Messages)-1]; last.Role != message.RoleAssistant || !strings.Contains(last.Content, "Polaris is its first consumer") {
		t.Fatalf("last message = %+v, want the assistant answer", last)
	}

	// Both searches actually reached the store, so the results carry the notes.
	var sawNova, sawPolaris bool
	for _, m := range res.Messages {
		if m.Role != message.RoleTool {
			continue
		}
		sawNova = sawNova || strings.Contains(m.Content, "many front doors")
		sawPolaris = sawPolaris || strings.Contains(m.Content, "first harness consumer")
	}
	if !sawNova || !sawPolaris {
		t.Fatalf("tool results nova=%v polaris=%v, want both notes found", sawNova, sawPolaris)
	}

	// The app's own event type, mapped from proto.Event.
	assertEventCount(t, events, "tool_call", 2)
	assertEventCount(t, events, "tool_end", 2)
	var text strings.Builder
	for _, e := range events {
		if e.Kind == "text" {
			text.WriteString(e.Text)
		}
	}
	if !strings.Contains(text.String(), "first consumer") {
		t.Fatalf("text events = %q, want the streamed answer", text.String())
	}

	// Usage is summed over both responses, which is what the app reports.
	want := provider.Usage{PromptTokens: 30, CompletionTokens: 15, TotalTokens: 45, CostUSD: 0.003}
	if res.Usage.PromptTokens != want.PromptTokens || res.Usage.CompletionTokens != want.CompletionTokens ||
		res.Usage.TotalTokens != want.TotalTokens || res.Usage.CostUSD != want.CostUSD {
		t.Fatalf("Usage = %+v, want %+v", res.Usage, want)
	}

	// OnMessage persisted exactly the new messages and nothing twice.
	if got, wantLen := len(store.History()), len(res.New); got != wantLen {
		t.Fatalf("persisted history = %d messages, want %d (one per new transcript message)", got, wantLen)
	}
}

// TestEmbed_EarlyEndOnPendingQuestion is the Polaris PendingQuestion pattern:
// a tool cannot finish without the user, records that on the app's state, and
// the TurnEnd hook ends the run. It is observable because the turn still has
// tool calls: without the hook the loop would take another turn, so the
// spare scripted turn must survive unused.
func TestEmbed_EarlyEndOnPendingQuestion(t *testing.T) {
	clock := testkit.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "note_search", Args: `{"query":"nonexistent"}`}}},
		// This turn must never be requested: the hook ends the run first.
		testkit.Turn{Text: "SHOULD NOT RUN"},
	)
	store := demoStore()
	var events []Event

	res, err := newApp(p, store, NoteTools(), &events).Run(context.Background(), nil, "find my nonexistent note", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Turns != 1 {
		t.Fatalf("Turns = %d, want 1 (the hook ended the run after the tool turn)", res.Turns)
	}
	if left := p.Remaining(); left != 1 {
		t.Fatalf("provider turns left = %d, want 1: the run asked for another turn instead of stopping\n%+v", left, res.Messages)
	}
	if strings.Contains(res.Answer, "SHOULD NOT RUN") {
		t.Fatalf("Answer = %q, the spare turn ran", res.Answer)
	}
	// The tool result still reached the model and the transcript, so the app can
	// show what it looked at alongside the question it is waiting on.
	if got := countRole(res.Messages, message.RoleTool); got != 1 {
		t.Fatalf("tool messages = %d, want 1", got)
	}
	// The question was consumed, so a later turn is not ended by it.
	if q := store.TakePending(); q != "" {
		t.Fatalf("pending question still set after the run: %q", q)
	}
}

// TestEmbed_SecondRunOnSavedHistory proves the app owns history: the second run
// sends what the first persisted, and the harness does not re-persist it.
func TestEmbed_SecondRunOnSavedHistory(t *testing.T) {
	clock := testkit.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := demoStore()
	var events []Event
	tools := NoteTools()

	first := testkit.NewProvider(clock, twoToolTurns()...)
	if _, err := newApp(first, store, tools, &events).Run(context.Background(), store.History(), "what about nova?", false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	saved := store.History()
	if len(saved) == 0 {
		t.Fatal("nothing persisted by the first run")
	}

	// A read-only run offers only note_search; the answer needs no new tool.
	second := testkit.NewProvider(clock, testkit.Turn{Text: "Saved notes say hi."})
	res, err := newApp(second, store, tools, &events).Run(context.Background(), saved, "summarise", true)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	reqs := second.Requests()
	if len(reqs) != 1 {
		t.Fatalf("second provider saw %d requests, want 1", len(reqs))
	}
	// system + the saved history + the new prompt: nothing dropped, nothing doubled.
	if got, want := len(reqs[0].Messages), len(saved)+2; got != want {
		t.Fatalf("second request carried %d messages, want %d (system + history + prompt)", got, want)
	}
	if last := reqs[0].Messages[len(reqs[0].Messages)-1]; last.Content != "summarise" {
		t.Fatalf("last request message = %+v, want the new prompt", last)
	}
	if got, want := len(store.History()), len(saved)+len(res.New); got != want {
		t.Fatalf("history = %d after the second run, want %d (saved + new only)", got, want)
	}
	// A read-only run still finishes; scoping is a tool-view question, not a
	// loop question.
	if res.Answer != "Saved notes say hi." {
		t.Fatalf("Answer = %q, want the scripted answer", res.Answer)
	}
}

// TestEmbed_ReadOnlyViewHidesWriteTools checks Registry.View is what scopes the
// run: a read-only run is offered note_search and not note_save, and a call to
// a hidden tool comes back as an unknown-tool result rather than a crash.
func TestEmbed_ReadOnlyViewHidesWriteTools(t *testing.T) {
	clock := testkit.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	p := testkit.NewProvider(clock,
		testkit.Turn{ToolCalls: []testkit.Call{{ID: "c1", Name: "note_save", Args: `{"id":"n3","title":"x","body":"y"}`}}},
		testkit.Turn{Text: "Could not save."},
	)
	store := demoStore()
	var events []Event

	res, err := newApp(p, store, NoteTools(), &events).Run(context.Background(), nil, "save a note", true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var defs []string
	for _, d := range p.Requests()[0].Tools {
		defs = append(defs, d.Function.Name)
	}
	if len(defs) != 1 || defs[0] != "note_search" {
		t.Fatalf("offered tools = %v, want [note_search]", defs)
	}
	var refused bool
	for _, m := range res.Messages {
		if m.Role == message.RoleTool && strings.Contains(m.Content, "note_save") && strings.Contains(m.Content, "unknown") {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("hidden tool call was not refused as unknown:\n%+v", res.Messages)
	}
}

// TestEmbed_ConcurrentRunsShareRegistry runs two separate Agents over one
// registry and one store. This is the -race case a consumer hits when it serves
// two conversations at once, and the reason Registry.View copies instead of
// aliasing registration state.
func TestEmbed_ConcurrentRunsShareRegistry(t *testing.T) {
	tools := NoteTools()
	store := demoStore()
	clock := testkit.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	ans := make([]string, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := testkit.NewProvider(clock, testkit.Turn{Text: "answer"})
			var events []Event
			readOnly := i == 0
			res, err := newApp(p, store, tools, &events).Run(context.Background(), nil, "question", readOnly)
			errs[i] = err
			if res != nil {
				ans[i] = res.Answer
			}
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if ans[i] != "answer" {
			t.Fatalf("run %d answer = %q, want answer", i, ans[i])
		}
	}
}

func countRole(msgs []message.Message, role message.Role) int {
	n := 0
	for _, m := range msgs {
		if m.Role == role {
			n++
		}
	}
	return n
}

// findUserAfterLastTool returns the last user message, which is the nudge the
// TurnEnd hook injected after the tool batch.
func findUserAfterLastTool(msgs []message.Message) string {
	lastTool := -1
	for i, m := range msgs {
		if m.Role == message.RoleTool {
			lastTool = i
		}
	}
	for _, m := range msgs[lastTool+1:] {
		if m.Role == message.RoleUser {
			return m.Content
		}
	}
	return ""
}

func assertEventCount(t *testing.T, events []Event, kind string, want int) {
	t.Helper()
	got := 0
	for _, e := range events {
		if e.Kind == kind {
			got++
		}
	}
	if got != want {
		t.Fatalf("%s events = %d, want %d (%+v)", kind, got, want, events)
	}
}
