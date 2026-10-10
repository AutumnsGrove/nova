package compaction

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/session"
)

func FuzzEstimate(f *testing.F) {
	f.Add("hello world", "user")
	f.Add("", "assistant")
	f.Add("ééé", "")
	f.Fuzz(func(t *testing.T, content, role string) {
		m := message.Message{Role: message.Role(role), Content: content}
		if got := Estimate([]message.Message{m}); got < 0 {
			t.Fatalf("Estimate = %d, want non-negative", got)
		}
	})
}

func TestProperty_CMP_NeverLosesEntries(t *testing.T) {
	rng := rand.New(rand.NewSource(31337))
	path := filepath.Join(t.TempDir(), "s.jsonl")
	st, err := session.Open(path, session.Options{})
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	appendTurns(t, st, 8)

	fileSize := func() int64 {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat session: %v", err)
		}
		return info.Size()
	}

	for i := 0; i < 20; i++ {
		// Add a random amount of new work, then compact with a random keep.
		appendTurns(t, st, 1+rng.Intn(4))
		before := len(st.Entries())
		sizeBefore := fileSize()
		c := compactor(newFake(), Settings{KeepRecent: 100 + rng.Intn(2000)})
		_, err := Run(context.Background(), st, c, "")
		if err != nil && !errors.Is(err, ErrNothingToCompact) {
			t.Fatalf("iter %d: Run: %v", i, err)
		}
		after := len(st.Entries())
		if after < before || after > before+1 {
			t.Fatalf("iter %d: entries went %d -> %d", i, before, after)
		}
		if size := fileSize(); size < sizeBefore {
			t.Fatalf("iter %d: session file shrank %d -> %d", i, sizeBefore, size)
		}
	}
}
