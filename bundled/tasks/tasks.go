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
	mu    sync.Mutex
	env   *ext.Env
	tasks []Task
	next  int
}

// New returns a fresh tasks extension.
func New() *Extension { return &Extension{} }

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
	e.publish()
	return nil
}

// tool builds the model-facing todo tool.
func (e *Extension) tool() tool.Tool {
	info := tool.Info{
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
	}
	return tool.New(info, e.run)
}

// createInput, updateInput, and deleteInput are the action payloads.
type createInput struct {
	Action string `json:"action"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Tasks  []struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	} `json:"tasks"`
}

type updateInput struct {
	Action string  `json:"action"`
	ID     string  `json:"id"`
	Title  *string `json:"title"`
	Status *string `json:"status"`
	Detail *string `json:"detail"`
}

func (e *Extension) run(_ context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
	var head struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &head); err != nil {
		return tool.Errorf("invalid arguments: %v", err), nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var err error
	switch head.Action {
	case "create":
		err = e.create(args)
	case "update":
		err = e.update(args)
	case "delete":
		err = e.delete(args)
	case "list":
		// no change
	case "clear":
		e.tasks = nil
		e.persistLocked()
	default:
		return tool.Errorf("unknown action %q; actions are create, update, delete, list, clear", head.Action), nil
	}
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	return tool.Text("%s", e.resultLocked()), nil
}

// create makes one task or a batch of up to maxPerCreate.
func (e *Extension) create(args json.RawMessage) error {
	var in createInput
	if err := json.Unmarshal(args, &in); err != nil {
		return err
	}
	titles := in.Tasks
	if len(titles) == 0 {
		titles = []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		}{{Title: in.Title, Detail: in.Detail}}
	}
	if len(titles) > maxPerCreate {
		return fmt.Errorf("too many tasks in one call: %d, at most %d", len(titles), maxPerCreate)
	}
	if len(e.tasks)+len(titles) > maxTasks {
		return fmt.Errorf("too many tasks: %d, at most %d", len(e.tasks)+len(titles), maxTasks)
	}
	newTasks := make([]Task, 0, len(titles))
	for _, t := range titles {
		title := strings.TrimSpace(t.Title)
		if title == "" {
			return fmt.Errorf("title is required")
		}
		if len([]rune(title)) > maxTitle {
			return fmt.Errorf("title is over %d characters", maxTitle)
		}
		if len([]rune(t.Detail)) > maxDetail {
			return fmt.Errorf("detail is over %d characters", maxDetail)
		}
		newTasks = append(newTasks, Task{ID: e.newIDLocked(), Title: title, Status: statusTodo, Detail: t.Detail})
	}
	e.tasks = append(e.tasks, newTasks...)
	e.persistLocked()
	return nil
}

// update applies the provided fields to one task.
func (e *Extension) update(args json.RawMessage) error {
	var in updateInput
	if err := json.Unmarshal(args, &in); err != nil {
		return err
	}
	i := e.indexLocked(in.ID)
	if i < 0 {
		return fmt.Errorf("unknown task id %q", in.ID)
	}
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return fmt.Errorf("title is required")
		}
		if len([]rune(title)) > maxTitle {
			return fmt.Errorf("title is over %d characters", maxTitle)
		}
		e.tasks[i].Title = title
	}
	if in.Status != nil {
		if !validStatus(*in.Status) {
			return fmt.Errorf("unknown status %q; statuses are %s", *in.Status, statuses())
		}
		e.tasks[i].Status = *in.Status
	}
	if in.Detail != nil {
		if len([]rune(*in.Detail)) > maxDetail {
			return fmt.Errorf("detail is over %d characters", maxDetail)
		}
		e.tasks[i].Detail = *in.Detail
	}
	e.persistLocked()
	return nil
}

// delete removes a task.
func (e *Extension) delete(args json.RawMessage) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return err
	}
	i := e.indexLocked(in.ID)
	if i < 0 {
		return fmt.Errorf("unknown task id %q", in.ID)
	}
	e.tasks = append(e.tasks[:i], e.tasks[i+1:]...)
	e.persistLocked()
	return nil
}

// newIDLocked allocates the next id, never reusing a number.
func (e *Extension) newIDLocked() string {
	e.next++
	return fmt.Sprintf("t%d", e.next)
}

// indexLocked returns the index of id, or -1.
func (e *Extension) indexLocked(id string) int {
	for i, t := range e.tasks {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// resultLocked renders the whole list plus the one-at-a-time reminder.
func (e *Extension) resultLocked() string {
	out := listText(e.tasks)
	doing := 0
	for _, t := range e.tasks {
		if t.Status == statusDoing {
			doing++
		}
	}
	if doing > 1 {
		out += "\n" + fmt.Sprintf("note: %d tasks are doing; keep one in progress at a time", doing)
	}
	return out
}

// persistLocked appends a full snapshot to the session.
func (e *Extension) persistLocked() {
	if e.env == nil || e.env.Session == nil {
		return
	}
	data, err := encodeState(e.tasks, e.next)
	if err != nil {
		return
	}
	_ = e.env.Session.AppendEntry(stateKind, data, true)
	e.publishLocked()
}

// rebuild replaces the in-memory list from the last snapshot on the active
// branch. No snapshot means an empty list.
func (e *Extension) rebuild() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tasks = nil
	e.next = 0
	if e.env == nil || e.env.Session == nil {
		return
	}
	entries := e.env.Session.Entries(stateKind)
	if len(entries) == 0 {
		return
	}
	s, ok := decodeState(entries[len(entries)-1])
	if !ok {
		return
	}
	e.tasks = s.Tasks
	e.next = s.Next
}

// command implements `/todos` and `/todos clear`.
func (e *Extension) command(_ context.Context, args string) (string, error) {
	if strings.TrimSpace(args) == "clear" {
		e.mu.Lock()
		e.tasks = nil
		e.persistLocked()
		e.mu.Unlock()
		return "Tasks cleared.", nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resultLocked(), nil
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
	if len(e.tasks) == 0 {
		e.env.UI.Notify(proto.UIRequest{Method: "widget", Key: "tasks", Placement: "above_editor"})
		e.env.UI.Notify(proto.UIRequest{Method: "status", Key: "tasks"})
		return
	}
	done := 0
	items := make([]map[string]any, 0, len(e.tasks))
	for _, t := range e.tasks {
		if t.Status == statusDone {
			done++
		}
		items = append(items, map[string]any{"Text": t.Title, "State": t.Status, "Detail": t.Detail})
	}
	var content map[string]any
	if done == len(e.tasks) {
		content = map[string]any{"Kind": "text", "Lines": []string{fmt.Sprintf("☑ %d of %d tasks", done, len(e.tasks))}}
	} else {
		content = map[string]any{"Kind": "list", "Items": items}
	}
	data, _ := json.Marshal(content)
	e.env.UI.Notify(proto.UIRequest{Method: "widget", Key: "tasks", Placement: "above_editor", Content: data})
	e.env.UI.Notify(proto.UIRequest{Method: "status", Key: "tasks", Segments: []proto.UISegment{{Text: fmt.Sprintf("☐ %d of %d", done, len(e.tasks)), Style: "muted"}}})
}
