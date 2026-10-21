package tasks

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A rejected update must leave the list exactly as it was, including the
// fields that were valid and applied before the bad one was reached.
func TestTasks_TSK30_FailedUpdateChangesNothing(t *testing.T) {
	e, _ := setup(&fakeSession{}, &fakeUI{})
	mustCreate(t, e, "original")
	res, isErr := call(t, e, `{"action":"update","id":"t1","title":"renamed","status":"bogus"}`)
	if !isErr {
		t.Fatalf("update with a bad status succeeded: %q", res.Content)
	}
	if got := e.state.Tasks[0]; got.Title != "original" || got.Status != statusTodo {
		t.Fatalf("task after a rejected update = %+v", got)
	}

	// Apply is pure: the state it was given is never modified, error or not.
	before := State{V: stateVersion, Next: 2, Tasks: []Task{{ID: "t1", Title: "a", Status: statusTodo}, {ID: "t2", Title: "b", Status: statusTodo}}}
	if _, _, _, err := Apply(before, json.RawMessage(`{"action":"update","id":"t1","title":"x","status":"bogus"}`)); err == nil {
		t.Fatal("want an error")
	}
	if _, _, _, err := Apply(before, json.RawMessage(`{"action":"delete","id":"t1"}`)); err != nil {
		t.Fatal(err)
	}
	if len(before.Tasks) != 2 || before.Tasks[0].Title != "a" || before.Tasks[1].ID != "t2" {
		t.Fatalf("Apply modified its input: %+v", before.Tasks)
	}
}

// failingSession cannot store entries.
type failingSession struct{ fakeSession }

func (*failingSession) AppendEntry(string, json.RawMessage, bool) error {
	return errors.New("disk full")
}

// A change that cannot be saved must not be reported as done, or the model
// would build on a list that vanishes on resume.
func TestTasks_TSK31_UnsavedChangeIsRolledBack(t *testing.T) {
	e, _ := setup(&failingSession{}, &fakeUI{})
	res, isErr := call(t, e, `{"action":"create","title":"one"}`)
	if !isErr || !strings.Contains(res.Content, "disk full") {
		t.Fatalf("result = %q isErr=%v, want the save error", res.Content, isErr)
	}
	if len(e.state.Tasks) != 0 || e.state.Next != 0 {
		t.Fatalf("state after a failed save = %+v", e.state)
	}
}
