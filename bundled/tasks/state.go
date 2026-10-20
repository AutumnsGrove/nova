package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Limits on task count, batch size, and field lengths.
const (
	maxTasks      = 200
	maxPerCreate  = 50
	maxTitle      = 200
	maxDetail     = 2000
	stateKind     = "tasks.state"
	stateVersion  = 1
	statusTodo    = "todo"
	statusDoing   = "doing"
	statusDone    = "done"
	statusBlocked = "blocked"
)

// Task is one task.
type Task struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// State is the persisted full state of the task list, not a diff.
type State struct {
	V     int    `json:"v"`
	Next  int    `json:"next"`
	Tasks []Task `json:"tasks"`
}

// StateKind is the session entry kind the task snapshot is stored under.
func StateKind() string { return stateKind }

// NewState returns an empty state at the current version.
func NewState() State { return State{V: stateVersion} }

// validStatus reports whether s is one of the four statuses.
func validStatus(s string) bool {
	switch s {
	case statusTodo, statusDoing, statusDone, statusBlocked:
		return true
	}
	return false
}

// statuses lists the valid statuses, for an error message.
func statuses() string {
	return statusTodo + ", " + statusDoing + ", " + statusDone + ", " + statusBlocked
}

// Encode marshals a state.
func Encode(s State) (json.RawMessage, error) { return json.Marshal(s) }

// Decode parses a state, rejecting an unknown version.
func Decode(data json.RawMessage) (State, bool) {
	var s State
	if err := json.Unmarshal(data, &s); err != nil || s.V != stateVersion {
		return State{}, false
	}
	return s, true
}

// Actions lists the actions, for an unknown-action error.
func Actions() string { return "create, update, delete, list, clear" }

// Apply performs one todo action on a state and returns the new state, the
// text result, and whether the state changed. It is pure: it works on a copy,
// so the state it was given is never modified, and an error returns that state
// untouched.
func Apply(s State, args json.RawMessage) (State, string, bool, error) {
	var head struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &head); err != nil {
		return s, "", false, fmt.Errorf("invalid arguments: %w", err)
	}
	next := State{V: stateVersion, Next: s.Next, Tasks: slices.Clone(s.Tasks)}
	var (
		changed bool
		err     error
	)
	switch head.Action {
	case "create":
		changed, err = applyCreate(&next, args)
	case "update":
		changed, err = applyUpdate(&next, args)
	case "delete":
		changed, err = applyDelete(&next, args)
	case "list":
	case "clear":
		next.Tasks, changed = nil, true
	default:
		err = fmt.Errorf("unknown action %q; actions are %s", head.Action, Actions())
	}
	if err != nil {
		return s, "", false, err
	}
	return next, resultText(next), changed, nil
}

// applyCreate makes one task or a batch of up to maxPerCreate.
func applyCreate(s *State, args json.RawMessage) (bool, error) {
	var in struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Tasks  []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return false, err
	}
	items := in.Tasks
	if len(items) == 0 {
		items = []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		}{{Title: in.Title, Detail: in.Detail}}
	}
	if len(items) > maxPerCreate {
		return false, fmt.Errorf("too many tasks in one call: %d, at most %d", len(items), maxPerCreate)
	}
	if len(s.Tasks)+len(items) > maxTasks {
		return false, fmt.Errorf("too many tasks: %d, at most %d", len(s.Tasks)+len(items), maxTasks)
	}
	added := make([]Task, 0, len(items))
	for _, t := range items {
		title, err := cleanTitle(t.Title)
		if err != nil {
			return false, err
		}
		if err := checkDetail(t.Detail); err != nil {
			return false, err
		}
		s.Next++
		added = append(added, Task{ID: fmt.Sprintf("t%d", s.Next), Title: title, Status: statusTodo, Detail: t.Detail})
	}
	s.Tasks = append(s.Tasks, added...)
	return true, nil
}

// applyUpdate applies the provided fields to one task.
func applyUpdate(s *State, args json.RawMessage) (bool, error) {
	var in struct {
		ID     string  `json:"id"`
		Title  *string `json:"title"`
		Status *string `json:"status"`
		Detail *string `json:"detail"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return false, err
	}
	i := indexOf(s.Tasks, in.ID)
	if i < 0 {
		return false, fmt.Errorf("unknown task id %q", in.ID)
	}
	// Check every field before changing any, so a bad one leaves the task as it was.
	t := s.Tasks[i]
	if in.Title != nil {
		title, err := cleanTitle(*in.Title)
		if err != nil {
			return false, err
		}
		t.Title = title
	}
	if in.Status != nil {
		if !validStatus(*in.Status) {
			return false, fmt.Errorf("unknown status %q; statuses are %s", *in.Status, statuses())
		}
		t.Status = *in.Status
	}
	if in.Detail != nil {
		if err := checkDetail(*in.Detail); err != nil {
			return false, err
		}
		t.Detail = *in.Detail
	}
	s.Tasks[i] = t
	return true, nil
}

// cleanTitle trims a title and checks its length.
func cleanTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", errors.New("title is required")
	}
	if utf8.RuneCountInString(title) > maxTitle {
		return "", fmt.Errorf("title is over %d characters", maxTitle)
	}
	return title, nil
}

// checkDetail checks the length of a detail.
func checkDetail(detail string) error {
	if utf8.RuneCountInString(detail) > maxDetail {
		return fmt.Errorf("detail is over %d characters", maxDetail)
	}
	return nil
}

// applyDelete removes a task.
func applyDelete(s *State, args json.RawMessage) (bool, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return false, err
	}
	i := indexOf(s.Tasks, in.ID)
	if i < 0 {
		return false, fmt.Errorf("unknown task id %q", in.ID)
	}
	s.Tasks = slices.Delete(s.Tasks, i, i+1)
	return true, nil
}

// indexOf returns the index of an id, or -1.
func indexOf(tasks []Task, id string) int {
	return slices.IndexFunc(tasks, func(t Task) bool { return t.ID == id })
}

// resultText renders the whole list plus the one-at-a-time reminder.
func resultText(s State) string {
	out := listText(s.Tasks)
	doing := 0
	for _, t := range s.Tasks {
		if t.Status == statusDoing {
			doing++
		}
	}
	if doing > 1 {
		out += "\n" + fmt.Sprintf("note: %d tasks are doing; keep one in progress at a time", doing)
	}
	return out
}

// listText renders the whole list as text. Tasks keep insertion order.
func listText(tasks []Task) string {
	if len(tasks) == 0 {
		return "(no tasks)"
	}
	var b strings.Builder
	for i, t := range tasks {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(t.ID + " [" + t.Status + "] " + t.Title)
	}
	return b.String()
}
