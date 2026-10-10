package session

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/message"
)

// toolCallEntry builds an assistant message with one tool call.
func toolCallEntry(id, name, args string) Entry {
	return Entry{Type: TypeMessage, Message: &message.Message{
		Role:      message.RoleAssistant,
		ToolCalls: []message.ToolCall{{ID: id, Function: message.FunctionCall{Name: name, Arguments: args}}},
	}}
}

// toolResultEntry builds a tool message for a call.
func toolResultEntry(id, content string) Entry {
	return Entry{Type: TypeMessage, Message: &message.Message{Role: message.RoleTool, ToolCallID: id, Content: content}}
}

// contents returns the content of each message.
func contents(msgs []message.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func TestSession_CTX01_Simple(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(userEntry("u"))
	_, _ = s.Append(assistantEntry("a"))
	msgs, err := s.Messages("")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Role != message.RoleUser || msgs[1].Role != message.RoleAssistant {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestSession_CTX02_ToolPairs(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(userEntry("do it"))
	_, _ = s.Append(toolCallEntry("c1", "read", `{}`))
	_, _ = s.Append(toolResultEntry("c1", "done"))
	msgs, err := s.Messages("")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if err := message.Validate(msgs); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(msgs) != 3 || msgs[2].ToolCallID != "c1" {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestSession_CTX03_CompactionReplaces(t *testing.T) {
	s, _ := openStore(t, Options{})
	var ids []string
	for i := 0; i < 6; i++ {
		var e Entry
		if i%2 == 0 {
			e = userEntry("m" + string(rune('1'+i)))
		} else {
			e = assistantEntry("m" + string(rune('1'+i)))
		}
		ne, _ := s.Append(e)
		ids = append(ids, ne.ID)
	}
	_, _ = s.Append(Entry{Type: TypeCompaction, Summary: "SUM", FirstKept: ids[3]})
	msgs, err := s.Messages("")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	got := contents(msgs)
	if len(got) != 4 || !strings.Contains(got[0], "SUM") || got[1] != "m4" || got[2] != "m5" || got[3] != "m6" {
		t.Fatalf("context = %v, want summary then m4..m6", got)
	}
}

func TestSession_CTX04_AfterCompaction(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(userEntry("m1"))
	b, _ := s.Append(assistantEntry("m2"))
	_, _ = s.Append(Entry{Type: TypeCompaction, Summary: "SUM", FirstKept: b.ID})
	_, _ = s.Append(userEntry("m3"))
	msgs, _ := s.Messages("")
	got := contents(msgs)
	if len(got) != 3 || got[1] != "m2" || got[2] != "m3" {
		t.Fatalf("context = %v, want summary, m2, m3", got)
	}
}

func TestSession_CTX05_LastCompactionWins(t *testing.T) {
	s, _ := openStore(t, Options{})
	var ids []string
	roles := []message.Role{message.RoleUser, message.RoleAssistant, message.RoleUser, message.RoleAssistant, message.RoleUser, message.RoleAssistant}
	for i, r := range roles {
		ne, _ := s.Append(Entry{Type: TypeMessage, Message: &message.Message{Role: r, Content: "m" + string(rune('1'+i))}})
		ids = append(ids, ne.ID)
	}
	_, _ = s.Append(Entry{Type: TypeCompaction, Summary: "S1", FirstKept: ids[1]})
	_, _ = s.Append(Entry{Type: TypeCompaction, Summary: "S2", FirstKept: ids[4]})
	msgs, _ := s.Messages("")
	got := contents(msgs)
	if len(got) != 3 || !strings.Contains(got[0], "S2") || strings.Contains(got[0], "S1") || got[1] != "m5" || got[2] != "m6" {
		t.Fatalf("context = %v, want only the second compaction", got)
	}
}

func TestSession_CTX06_FirstKeptMissing(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(userEntry("m1"))
	_, _ = s.Append(assistantEntry("m2"))
	_, _ = s.Append(Entry{Type: TypeCompaction, Summary: "SUM", FirstKept: "nope"})
	msgs, _ := s.Messages("")
	if got := contents(msgs); len(got) != 1 || !strings.Contains(got[0], "SUM") {
		t.Fatalf("context = %v, want summary only", got)
	}
	_, _ = s.Append(userEntry("m3"))
	msgs, _ = s.Messages("")
	got := contents(msgs)
	if len(got) != 2 || !strings.Contains(got[0], "SUM") || got[1] != "m3" {
		t.Fatalf("context = %v, want summary and later messages", got)
	}
}

func TestSession_CTX07_SummaryShape(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(Entry{Type: TypeCompaction, Summary: "hello"})
	msgs, _ := s.Messages("")
	if len(msgs) != 1 || msgs[0].Role != message.RoleUser {
		t.Fatalf("msgs = %+v", msgs)
	}
	if msgs[0].Content != "<summary>\nhello\n</summary>" {
		t.Fatalf("summary = %q", msgs[0].Content)
	}
}

func TestSession_CTX08_CustomMessageInContext(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(Entry{Type: TypeCustomMessage, Content: "note", InContext: true})
	msgs, _ := s.Messages("")
	if len(msgs) != 1 || msgs[0].Role != message.RoleUser || msgs[0].Content != "note" {
		t.Fatalf("msgs = %+v, want the custom message included", msgs)
	}
}

func TestSession_CTX09_CustomMessageExcluded(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(Entry{Type: TypeCustomMessage, Content: "note", InContext: false})
	msgs, _ := s.Messages("")
	if len(msgs) != 0 {
		t.Fatalf("msgs = %+v, want the display-only message excluded", msgs)
	}
}

func TestSession_CTX10_NonContextEntriesExcluded(t *testing.T) {
	s, _ := openStore(t, Options{})
	for _, e := range []Entry{
		{Type: TypeCustom, Kind: "x"},
		{Type: TypeLabel, Label: "l"},
		{Type: TypeSessionInfo, Name: "n"},
		{Type: TypeModelChange, Model: "m"},
		{Type: "future"},
	} {
		if _, err := s.Append(e); err != nil {
			t.Fatalf("Append %s: %v", e.Type, err)
		}
	}
	msgs, _ := s.Messages("")
	if len(msgs) != 0 {
		t.Fatalf("msgs = %+v, want none", msgs)
	}
}

func TestSession_CTX11_BranchSummary(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(Entry{Type: TypeBranchSummary, Summary: "branch text"})
	msgs, _ := s.Messages("")
	if len(msgs) != 1 || msgs[0].Role != message.RoleUser {
		t.Fatalf("msgs = %+v", msgs)
	}
	if msgs[0].Content != "<branch_summary>\nbranch text\n</branch_summary>" {
		t.Fatalf("content = %q", msgs[0].Content)
	}
}

func TestSession_CTX12_OrphanResultRepaired(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(userEntry("u"))
	_, _ = s.Append(toolResultEntry("ghost", "x"))
	msgs, _ := s.Messages("")
	if err := message.Validate(msgs); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "u" {
		t.Fatalf("msgs = %+v, want the orphan removed", msgs)
	}
	if len(s.Warnings()) == 0 {
		t.Fatal("want a repair warning")
	}
}

func TestSession_CTX13_MissingResultRepaired(t *testing.T) {
	s, _ := openStore(t, Options{})
	_, _ = s.Append(userEntry("u"))
	_, _ = s.Append(toolCallEntry("c1", "read", "{}"))
	msgs, _ := s.Messages("")
	if err := message.Validate(msgs); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(msgs) != 3 || msgs[2].Role != message.RoleTool || msgs[2].Content != "(result unavailable)" {
		t.Fatalf("msgs = %+v, want a placeholder result", msgs)
	}
	if len(s.Warnings()) == 0 {
		t.Fatal("want a repair warning")
	}
}

func TestSession_CTX15_KeepCustomBeforeCut(t *testing.T) {
	s, _ := openStore(t, Options{})
	a, _ := s.Append(userEntry("m1"))
	_, _ = s.Append(Entry{Type: TypeCustom, Kind: "todo", Data: []byte(`{"x":1}`), Keep: true})
	b, _ := s.Append(assistantEntry("m2"))
	_, _ = s.Append(Entry{Type: TypeCompaction, Summary: "SUM", FirstKept: b.ID})
	branch, _ := s.Branch("")
	if len(branch) != 4 {
		t.Fatalf("branch = %d entries, want the keep entry visible", len(branch))
	}
	msgs, _ := s.Messages("")
	for _, m := range msgs {
		if strings.Contains(m.Content, "todo") {
			t.Fatal("a keep custom entry must not enter the context")
		}
	}
	_ = a
}

func TestSession_CTX16_SideBranch(t *testing.T) {
	s, _ := openStore(t, Options{})
	m1, _ := s.Append(userEntry("1"))
	m2, _ := s.Append(assistantEntry("2"))
	_ = s.SetLeaf(m1.ID)
	_, _ = s.Append(userEntry("3"))
	got, _ := s.Messages("")
	if strings.Join(contents(got), ",") != "1,3" {
		t.Fatalf("current branch = %v, want 1,3", contents(got))
	}
	got, _ = s.Messages(m2.ID)
	if strings.Join(contents(got), ",") != "1,2" {
		t.Fatalf("side branch = %v, want 1,2", contents(got))
	}
}

func TestSession_CTX17_Empty(t *testing.T) {
	s, _ := openStore(t, Options{})
	msgs, err := s.Messages("")
	if err != nil || len(msgs) != 0 {
		t.Fatalf("msgs = %v err = %v, want empty", msgs, err)
	}
}

func TestProperty_CTX14_AlwaysValid(t *testing.T) {
	for seed := int64(0); seed < 10; seed++ {
		s, _ := openStore(t, Options{})
		rng := rand.New(rand.NewSource(seed))
		ids := []string{}
		for i := 0; i < 60; i++ {
			var e Entry
			switch rng.Intn(6) {
			case 0:
				e = userEntry("u")
			case 1:
				e = assistantEntry("a")
			case 2:
				e = toolCallEntry("c"+string(rune('a'+i%26)), "read", "{}")
			case 3:
				e = toolResultEntry("c"+string(rune('a'+rng.Intn(26))), "r")
			case 4:
				e = Entry{Type: TypeCustomMessage, Content: "note", InContext: rng.Intn(2) == 0}
			case 5:
				e = Entry{Type: TypeCompaction, Summary: "S", FirstKept: ""}
				if len(ids) > 0 {
					e.FirstKept = ids[rng.Intn(len(ids))]
				}
			}
			if rng.Intn(3) == 0 && len(ids) > 0 {
				ne, err := s.AppendOn(ids[rng.Intn(len(ids))], e)
				if err != nil {
					t.Fatalf("seed %d AppendOn: %v", seed, err)
				}
				ids = append(ids, ne.ID)
			} else {
				ne, err := s.Append(e)
				if err != nil {
					t.Fatalf("seed %d Append: %v", seed, err)
				}
				ids = append(ids, ne.ID)
			}
		}
		msgs, err := s.Messages("")
		if err != nil {
			t.Fatalf("seed %d Messages: %v", seed, err)
		}
		if err := message.Validate(msgs); err != nil {
			t.Fatalf("seed %d Validate: %v", seed, err)
		}
	}
}
