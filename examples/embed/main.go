// Command embed is a standalone program that uses Nova as a library: it imports
// the harness layer only (agent, tool, provider, message, proto) and none of the
// application shell (mode, config, session, tui, bundled).
//
// It is the proof behind docs/INTEGRATION.md. A consumer like Polaris or her-go
// keeps its own store, its own tools, its own prompts, and its own event type,
// and replaces only its bespoke loop. Everything here is deliberately small and
// is exercised by the test in this package.
//
// To run it against a real model:
//
//	OPENROUTER_API_KEY=sk-or-... go run ./examples/embed "what notes about nova do I have?"
//
// Without a key it explains what is missing and exits. The behaviour worth
// reading is in Run and turnEnd, and the test in this package drives both
// end to end against a scripted provider, so nothing here needs the network to
// be exercised.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/AutumnsGrove/nova/agent"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/openrouter"
	"github.com/AutumnsGrove/nova/tool"
)

// Note is the app's own domain type. Nova has no opinion about it.
type Note struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Store is the app's state: the notes and the conversation history, both of
// which the harness never owns. History is a slice here to stand in for a
// database; a real consumer would write rows.
type Store struct {
	mu      sync.Mutex
	notes   []Note
	history []message.Message
	// pending is set by a tool that could not finish without the user. It is
	// app state on ToolData, and TurnEnd reads it to stop the run -- the
	// Polaris PendingQuestion pattern.
	pending string
}

// SetPending records that the run is waiting on the user, in place of asking
// the model again.
func (s *Store) SetPending(q string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = q
}

// TakePending returns the question the run is waiting on and clears it, so a
// later turn is not ended by a question already handled.
func (s *Store) TakePending() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.pending
	s.pending = ""
	return q
}

// Search returns the notes matching q in title or body, case-insensitively.
func (s *Store) Search(q string) []Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	q = strings.ToLower(q)
	var out []Note
	for _, n := range s.notes {
		if strings.Contains(strings.ToLower(n.Title), q) || strings.Contains(strings.ToLower(n.Body), q) {
			out = append(out, n)
		}
	}
	return out
}

// Save stores a note, replacing one with the same id.
func (s *Store) Save(n Note) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.notes {
		if s.notes[i].ID == n.ID {
			s.notes[i] = n
			return
		}
	}
	s.notes = append(s.notes, n)
}

// Persist is OnMessage: it records each message as the run produces it, which is
// how the app keeps the transcript the harness handed it. usage is nil for a
// message that came from a tool or a hook rather than a provider response.
func (s *Store) Persist(m message.Message, _ *provider.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, m)
	return nil
}

// History returns a copy of the persisted transcript, for the next Run.
func (s *Store) History() []message.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]message.Message(nil), s.history...)
}

// Event is the app's own event type. Config.Emit receives the wire type
// proto.Event; mapping it here keeps the app's UI from depending on the wire.
type Event struct {
	Kind string
	Text string
}

// App wires a provider the app built to a registry the app filled. Nothing here
// comes from config.Config or nova login.
type App struct {
	Provider provider.Provider
	Model    string
	Tools    *tool.Registry
	Store    *Store
	Emit     func(Event)
}

// Run drives one run: a prompt and however many turns it takes to answer it.
// history is the app's own slice; the harness neither owns it nor writes it,
// because OnMessage is what persists.
//
// readOnly offers a different tool subset for this run through Registry.View,
// which is how a consumer scopes tools per agent without building a second
// registry.
func (a *App) Run(ctx context.Context, history []message.Message, prompt string, readOnly bool) (*agent.Result, error) {
	tools := a.Tools
	if readOnly {
		tools = a.Tools.View("note_search")
	}

	cfg := agent.Config{
		Provider: a.Provider,
		Model:    a.Model,
		System:   "You are a notes assistant. Search before you answer, and save only what the user asks you to save.",
		Tools:    tools,
		// The app's dependency bundle: tools type-assert it back to *Store.
		// Nova never imports it.
		ToolData:  a.Store,
		Emit:      a.emit,
		OnMessage: a.Store.Persist,
		Hooks: agent.Hooks{
			TurnEnd: a.turnEnd,
		},
	}

	return agent.New(cfg).Run(ctx, history, message.Message{Role: message.RoleUser, Content: prompt})
}

// emit maps the wire event to the app's event. A consumer with its own bus
// publishes the mapped value; only the types it cares about appear here.
func (a *App) emit(ev proto.Event) {
	if a.Emit == nil {
		return
	}
	switch ev.Type {
	case proto.EventTextDelta:
		a.Emit(Event{Kind: "text", Text: ev.Text})
	case proto.EventToolCall:
		if ev.Call != nil {
			a.Emit(Event{Kind: "tool_call", Text: ev.Call.Name})
		}
	case proto.EventToolEnd:
		if ev.Result != nil {
			a.Emit(Event{Kind: "tool_end", Text: ev.Result.Content})
		}
	}
}

// turnEnd is where the app's own control policy lives, the pattern Polaris uses
// for its pending question.
//
//   - A tool that could not finish without the user sets a question on the
//     app's state. Ending here is what stops the run, because the turn still
//     has tool calls and would otherwise ask the model for another turn.
//   - A turn that ran tools but produced no text gets a nudge, so the model
//     reports what it found instead of trailing off after the batch.
func (a *App) turnEnd(_ context.Context, t agent.Turn) agent.TurnDecision {
	if a.Store.TakePending() != "" {
		return agent.TurnDecision{End: true}
	}
	if len(t.Results) > 0 && strings.TrimSpace(t.Message.Content) == "" {
		return agent.TurnDecision{Inject: []message.Message{
			{Role: message.RoleUser, Content: "Report what you found in one short answer."},
		}}
	}
	return agent.TurnDecision{}
}

// NoteTools builds the app's two domain tools. Both read their dependency out of
// tool.Context.Data, which is the only channel the harness provides and all a
// consumer needs.
func NoteTools() *tool.Registry {
	reg := tool.NewRegistry()
	reg.MustRegister(tool.New(tool.Info{
		Name:        "note_search",
		Description: "Search the user's notes. Use before answering any question about them.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "text to match in a title or body"},
			},
			"required": []string{"query"},
		},
		Parallel: true,
		ReadOnly: true,
	}, searchNotes))
	reg.MustRegister(tool.New(tool.Info{
		Name:        "note_save",
		Description: "Save a note under an id. Saves over an existing id.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":    map[string]any{"type": "string"},
				"title": map[string]any{"type": "string"},
				"body":  map[string]any{"type": "string"},
			},
			"required": []string{"id", "title", "body"},
		},
		// Writes must not race a parallel search in the same batch.
		Parallel: false,
	}, saveNote))
	return reg
}

func searchNotes(_ context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Errorf("bad arguments: %v", err), nil
	}
	// Type-assert the bundle the app put on ToolData. A mismatch is a wiring bug,
	// so report it to the model as a failed call rather than panicking.
	store, ok := tctx.Data.(*Store)
	if !ok {
		return tool.Errorf("note store not wired"), nil
	}
	hits := store.Search(in.Query)
	if len(hits) == 0 {
		// Nothing matched: the run cannot continue without knowing which note the
		// user meant, so it asks and the TurnEnd hook ends the turn.
		store.SetPending(fmt.Sprintf("No notes matched %q. Which one did you mean?", in.Query))
		return tool.Text("no notes matched %q", in.Query), nil
	}
	var b strings.Builder
	for _, n := range hits {
		fmt.Fprintf(&b, "- %s: %s (%s)\n", n.ID, n.Title, n.Body)
	}
	return tool.Text("%s", b.String()), nil
}

func saveNote(_ context.Context, args json.RawMessage, tctx *tool.Context) (tool.Result, error) {
	var in Note
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Errorf("bad arguments: %v", err), nil
	}
	store, ok := tctx.Data.(*Store)
	if !ok {
		return tool.Errorf("note store not wired"), nil
	}
	store.Save(in)
	return tool.Text("saved %s", in.ID), nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, `usage: embed "your question"`)
		os.Exit(2)
	}
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "OPENROUTER_API_KEY is not set; set it to run the example against a model.")
		os.Exit(1)
	}
	store := &Store{notes: []Note{
		{ID: "n1", Title: "Nova", Body: "one binary, one JSONL protocol, many front doors"},
		{ID: "n2", Title: "Polaris", Body: "first production consumer of the harness layer"},
	}}
	app := &App{
		Provider: openrouter.New(openrouter.Config{APIKey: key}),
		Model:    "anthropic/claude-sonnet-4",
		Tools:    NoteTools(),
		Store:    store,
		Emit:     func(e Event) { fmt.Printf("[%s] %s\n", e.Kind, e.Text) },
	}

	res, err := app.Run(context.Background(), store.History(), os.Args[1], false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}
	fmt.Println("\nanswer:", res.Answer)
	fmt.Printf("usage: %d prompt + %d completion tokens, $%.4f over %d turns\n",
		res.Usage.PromptTokens, res.Usage.CompletionTokens, res.Usage.CostUSD, res.Turns)
}
