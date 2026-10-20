package session

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/AutumnsGrove/nova/message"
)

func FuzzOpen(f *testing.F) {
	f.Add([]byte(`{"type":"message","id":"a","message":{"role":"user","content":"a"}}` + "\n" +
		"garbage\n" +
		`{"type":"message","id":"b","message":{"role":"user","content":"b"}}` + "\n"))
	f.Add([]byte(`{"type":"message","id":"a","parent":"missing"}` + "\n"))
	f.Add([]byte("{"))
	f.Add([]byte(`{"type":"message","id":"a","parent":"a"}` + "\n"))
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 1<<16 {
			t.Skip()
		}
		var want []string
		seen := map[string]bool{}
		for _, line := range bytes.Split(body, []byte{'\n'}) {
			var probe struct {
				Type    string
				ID      string
				Message *message.Message
			}
			if json.Unmarshal(line, &probe) != nil || probe.Type != TypeMessage || probe.ID == "" || probe.Message == nil {
				continue
			}
			if !seen[probe.ID] {
				seen[probe.ID] = true
				want = append(want, probe.ID)
			}
		}
		path := filepath.Join(dir, "f.jsonl")
		content := append([]byte(`{"type":"session","v":1,"id":"s"}`+"\n"), body...)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		s, err := Open(path, Options{})
		if err != nil {
			return
		}
		defer s.Close()
		ids := map[string]bool{}
		for _, e := range s.Entries() {
			if e.ID == "" {
				t.Fatal("an entry survived without an id")
			}
			if ids[e.ID] {
				t.Fatalf("duplicate id %q survived", e.ID)
			}
			ids[e.ID] = true
		}
		for _, id := range want {
			if _, ok := s.Get(id); !ok {
				t.Fatalf("lost a valid entry %q that preceded a corrupt line", id)
			}
		}
		if _, err := s.Branch(""); err != nil {
			t.Fatalf("Branch: %v", err)
		}
	})
}

// TestProperty_SES_AppendOnly asserts that no operation rewrites bytes already
// in the file: every later file state has the previous state as a prefix.
func TestProperty_SES_AppendOnly(t *testing.T) {
	s, path := openStore(t, Options{})
	rng := rand.New(rand.NewSource(3))
	var ids []string
	var prev []byte
	for i := 0; i < 150; i++ {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if prev != nil && !bytes.HasPrefix(before, prev) {
			t.Fatalf("iteration %d rewrote earlier bytes", i)
		}
		prev = before
		switch rng.Intn(4) {
		case 0:
			if len(ids) > 0 {
				_ = s.SetLeaf(ids[rng.Intn(len(ids))])
			}
		case 1:
			e, err := s.Append(assistantEntry("a"))
			if err != nil {
				t.Fatalf("Append: %v", err)
			}
			ids = append(ids, e.ID)
		default:
			parent := ""
			if len(ids) > 0 {
				parent = ids[rng.Intn(len(ids))]
			}
			e, err := s.AppendOn(parent, userEntry("u"))
			if err != nil {
				t.Fatalf("AppendOn: %v", err)
			}
			ids = append(ids, e.ID)
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.HasPrefix(after, prev) {
		t.Fatal("the final append rewrote earlier bytes")
	}
}
