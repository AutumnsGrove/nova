package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"

	"github.com/AutumnsGrove/nova/ext"
)

// FuzzTaskOps applies an arbitrary operation sequence and checks the
// invariants: unique ids, within the task limit, and a decodable State.
func FuzzTaskOps(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4})
	f.Add([]byte{0, 0, 1, 1, 1, 3})
	f.Add([]byte{2, 2, 2})
	f.Fuzz(func(t *testing.T, ops []byte) {
		sess := &fakeSession{}
		e, _ := setup(sess, nil)
		for i, op := range ops {
			var body string
			switch op % 5 {
			case 0:
				body = fmt.Sprintf(`{"action":"create","title":"task %d"}`, i)
			case 1:
				body = fmt.Sprintf(`{"action":"update","id":"t1","status":"doing"}`)
			case 2:
				body = `{"action":"delete","id":"t1"}`
			case 3:
				body = `{"action":"clear"}`
			case 4:
				body = `{"action":"list"}`
			}
			_, _ = e.run(context.Background(), json.RawMessage(body), nil)
			if len(e.state.Tasks) > maxTasks {
				t.Fatalf("task count %d over limit", len(e.state.Tasks))
			}
			seen := map[string]bool{}
			for _, task := range e.state.Tasks {
				if seen[task.ID] {
					t.Fatalf("duplicate id %s", task.ID)
				}
				seen[task.ID] = true
			}
			if _, err := Encode(e.state); err != nil {
				t.Fatalf("State not encodable: %v", err)
			}
		}
	})
}

// TestProperty_TSK_SnapshotRebuild checks that a rebuilt extension equals the
// last State on the active branch after any operations.
func TestProperty_TSK_SnapshotRebuild(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 100; round++ {
		sess := &fakeSession{}
		e, _ := setup(sess, nil)
		ops := rng.Intn(20)
		for i := 0; i < ops; i++ {
			switch rng.Intn(4) {
			case 0:
				_, _ = e.run(context.Background(), json.RawMessage(fmt.Sprintf(`{"action":"create","title":"t%d"}`, i)), nil)
			case 1:
				_, _ = e.run(context.Background(), json.RawMessage(`{"action":"update","id":"t1","status":"done"}`), nil)
			case 2:
				_, _ = e.run(context.Background(), json.RawMessage(`{"action":"delete","id":"t1"}`), nil)
			case 3:
				_, _ = e.run(context.Background(), json.RawMessage(`{"action":"clear"}`), nil)
			}
		}
		rebuilt, _ := setup(sess, nil)
		if len(rebuilt.state.Tasks) != len(e.state.Tasks) || rebuilt.state.Next != e.state.Next {
			t.Fatalf("round %d: rebuilt %+v next=%d, want %+v next=%d", round, rebuilt.state.Tasks, rebuilt.state.Next, e.state.Tasks, e.state.Next)
		}
	}
}

// ensure the fake session satisfies ext.SessionAPI.
var _ ext.SessionAPI = (*fakeSession)(nil)
