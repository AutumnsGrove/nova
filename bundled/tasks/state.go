// Package tasks is the bundled todo extension: a `todo` tool, a `/todos`
// command, a widget above the editor, and a footer status segment. State lives
// in the session as a full snapshot after every change.
package tasks

import (
	"encoding/json"
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

// snapshot is the persisted full state, not a diff.
type snapshot struct {
	V     int    `json:"v"`
	Next  int    `json:"next"`
	Tasks []Task `json:"tasks"`
}

// validStatus reports whether s is one of the four statuses.
func validStatus(s string) bool {
	switch s {
	case statusTodo, statusDoing, statusDone, statusBlocked:
		return true
	}
	return false
}

// statuses lists the valid statuses in order, for an error message.
func statuses() string {
	return statusTodo + ", " + statusDoing + ", " + statusDone + ", " + statusBlocked
}
func encodeState(tasks []Task, next int) (json.RawMessage, error) {
	return json.Marshal(snapshot{V: stateVersion, Next: next, Tasks: tasks})
}

// decodeState parses a snapshot, rejecting an unknown version.
func decodeState(data json.RawMessage) (snapshot, bool) {
	var s snapshot
	if err := json.Unmarshal(data, &s); err != nil || s.V != stateVersion {
		return snapshot{}, false
	}
	return s, true
}

// listText renders the whole list as text. Tasks keep insertion order.
func listText(tasks []Task) string {
	if len(tasks) == 0 {
		return "(no tasks)"
	}
	out := ""
	for i, t := range tasks {
		if i > 0 {
			out += "\n"
		}
		out += t.ID + " [" + t.Status + "] " + t.Title
	}
	return out
}

// sortedStatuses returns the statuses in a stable order for tests.
func sortedStatuses() []string {
	return []string{statusTodo, statusDoing, statusDone, statusBlocked}
}
