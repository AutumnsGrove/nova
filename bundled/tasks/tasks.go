package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tool"
)

// Extension is the tasks bundled extension. It is safe for concurrent use.
type Extension struct {
	mu  sync.Mutex
	env *ext.Env
	// state is the in-memory copy of the session snapshot.
	state State
}

// New returns a fresh tasks extension.
func New() *Extension { return &Extension{state: NewState()} }

// Name is the extension name, matched by config extensions.disable.
func (e *Extension) Name() string { return "tasks" }

// Init registers the tool, the command, and the branch observer, then rebuilds
// state from the active branch.
func (e *Extension) Init(env *ext.Env) error {
	e.env = env
	if err := env.Tool(e.tool()); err != nil {
		return err
	}
	if err := env.Command(ext.InProcCommand{Name: "todos", Description: "Show or clear the task list", Run: e.command}); err != nil {
		return err
	}
	env.Events(func(ev proto.Event) {
		if ev.Type == proto.EventSessionChanged {
			e.rebuild()
			e.publish()
		}
	})
	e.rebuild()
	e.mu.Lock()
	has := len(e.state.Tasks) > 0
	e.mu.Unlock()
	if has {
		e.publish()
	}
	return nil
}

// tool builds the model-facing todo tool.
func (e *Extension) tool() tool.Tool {
	return tool.New(tool.Info{
		Name: "todo",
		Description: "Manage a task list. Every call returns the whole list. " +
			"More than one task may be doing; keep one in progress at a time.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"create", "update", "delete", "list", "clear"}},
				"id":     map[string]any{"type": "string"},
				"title":  map[string]any{"type": "string"},
				"status": map[string]any{"type": "string", "enum": []string{statusTodo, statusDoing, statusDone, statusBlocked}},
				"detail": map[string]any{"type": "string"},
				"tasks": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":       "object",
						"properties": map[string]any{"title": map[string]any{"type": "string"}, "detail": map[string]any{"type": "string"}},
					},
				},
			},
			"required": []string{"action"},
		},
		ReadOnly: false,
	}, e.run)
}

// run applies one action under the lock and persists a snapshot on a change.
func (e *Extension) run(_ context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	next, text, changed, err := Apply(e.state, args)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if changed {
		if err := e.commitLocked(next); err != nil {
			return tool.Errorf("%v", err), nil
		}
	}
	return tool.Text("%s", text), nil
}

// commitLocked saves next as the session snapshot and, only if that worked,
// makes it the current state and republishes the widget. A change that cannot
// be saved would vanish on resume, so it must not be reported as done.
func (e *Extension) commitLocked(next State) error {
	if e.env != nil && e.env.Session != nil {
		data, err := Encode(next)
		if err != nil {
			return fmt.Errorf("could not save the task list: %w", err)
		}
		if err := e.env.Session.AppendEntry(stateKind, data, true); err != nil {
			return fmt.Errorf("could not save the task list: %w", err)
		}
	}
	e.state = next
	e.publishLocked()
	return nil
}

// rebuild replaces the in-memory state from the last snapshot on the active
// branch. No snapshot means an empty list.
func (e *Extension) rebuild() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = NewState()
	if e.env == nil || e.env.Session == nil {
		return
	}
	entries := e.env.Session.Entries(stateKind)
	if len(entries) == 0 {
		return
	}
	if s, ok := Decode(entries[len(entries)-1]); ok {
		e.state = s
	}
}

// command implements `/todos` and `/todos clear`.
func (e *Extension) command(_ context.Context, args string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if strings.TrimSpace(args) != "clear" {
		return resultText(e.state), nil
	}
	cleared := e.state
	cleared.Tasks = nil
	if err := e.commitLocked(cleared); err != nil {
		return "", err
	}
	return "Tasks cleared.", nil
}

// publish sends the widget and status, taking the lock.
func (e *Extension) publish() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.publishLocked()
}

// publishLocked sends the widget and status. An empty list clears both.
func (e *Extension) publishLocked() {
	if e.env == nil || e.env.UI == nil {
		return
	}
	tasks := e.state.Tasks
	if len(tasks) == 0 {
		e.env.UI.Notify(proto.UIRequest{Method: "widget", Key: "tasks", Placement: "above_editor"})
		e.env.UI.Notify(proto.UIRequest{Method: "status", Key: "tasks"})
		return
	}
	done := 0
	items := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		if t.Status == statusDone {
			done++
		}
		items = append(items, map[string]any{"Text": t.Title, "State": t.Status, "Detail": t.Detail})
	}
	var content map[string]any
	if done == len(tasks) {
		content = map[string]any{"Kind": "text", "Lines": []string{fmt.Sprintf("☑ %d of %d tasks", done, len(tasks))}}
	} else {
		content = map[string]any{"Kind": "list", "Items": items}
	}
	data, _ := json.Marshal(content)
	e.env.UI.Notify(proto.UIRequest{Method: "widget", Key: "tasks", Placement: "above_editor", Content: data})
	e.env.UI.Notify(proto.UIRequest{Method: "status", Key: "tasks", Segments: []proto.UISegment{{Text: fmt.Sprintf("☐ %d of %d", done, len(tasks)), Style: "muted"}}})
}

// ToolNames lists the tool Init registers.
func (e *Extension) ToolNames() []string { return []string{"todo"} }
